package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	telemetryv1 "gpu-metric-collector/api/gen"
	"gpu-metric-collector/internal/model"
	"gpu-metric-collector/internal/storage"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	flagBroker       = flag.String("broker", "127.0.0.1:9000", "Broker gRPC address")
	flagGroup        = flag.String("group", "default", "Consumer group")
	flagBatchSize    = flag.Int("batch", 500, "Collector batch size")
	flagFlushMs      = flag.Int("flush_ms", 1000, "Max flush interval in ms")
	flagWorkers      = flag.Int("workers", 4, "Flush worker count")
	flagMetrics      = flag.String("metrics_addr", ":9102", "Metrics HTTP listen address")
	flagInfluxURL    = flag.String("influx_url", "", "InfluxDB URL, e.g. http://localhost:8086")
	flagInfluxOrg    = flag.String("influx_org", "", "InfluxDB organization")
	flagInfluxBucket = flag.String("influx_bucket", "", "InfluxDB bucket")
	flagInfluxToken  = flag.String("influx_token", "", "InfluxDB API token")
	flagShutdownMs   = flag.Int("shutdown_timeout_ms", 5000, "Max time to wait for flush workers on shutdown (ms)")
)

var (
	metricReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "collector", Name: "messages_received_total", Help: "Messages received from broker.",
	})
	metricBatched = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "collector", Name: "messages_batched_total", Help: "Messages added to a batch.",
	})
	metricFlushed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "collector", Name: "messages_flushed_total", Help: "Messages flushed to storage.",
	})
	metricDroppedInvalid = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "collector", Name: "messages_dropped_invalid_total", Help: "Messages dropped due to validation.",
	})
	metricFlushErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "collector", Name: "flush_errors_total", Help: "Errors during flush to storage.",
	})
	metricBacklog = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "gpu_telerology", Subsystem: "collector", Name: "backlog", Help: "Current in-memory batch size.",
	})
	metricFlushLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "gpu_telemetry", Subsystem: "collector", Name: "flush_latency_seconds", Help: "Latency of batch flush to storage.",
		Buckets: prometheus.DefBuckets,
	})
)

func init() {
	prometheus.MustRegister(metricReceived, metricBatched, metricFlushed, metricDroppedInvalid, metricFlushErrors, metricBacklog, metricFlushLatency)
}

func main() {
	flag.Parse()

	http.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Printf("collector: metrics on %s", *flagMetrics)
		_ = http.ListenAndServe(*flagMetrics, nil)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; log.Printf("collector: shutdown signal"); cancel() }()

	if err := run(ctx); err != nil {
		log.Fatalf("collector error: %v", err)
	}
}

func run(ctx context.Context) error {
	var store storage.Store
	// Prefer InfluxDB if configured; otherwise use in-memory
	if stringsTrim(*flagInfluxURL) != "" && stringsTrim(*flagInfluxOrg) != "" && stringsTrim(*flagInfluxBucket) != "" && stringsTrim(*flagInfluxToken) != "" {
		s, err := storage.NewInfluxStore(stringsTrim(*flagInfluxURL), stringsTrim(*flagInfluxOrg), stringsTrim(*flagInfluxBucket), stringsTrim(*flagInfluxToken))
		if err != nil {
			return fmt.Errorf("open influx store: %w", err)
		}
		store = s
		log.Printf("collector: using influx store url=%s org=%s bucket=%s", *flagInfluxURL, *flagInfluxOrg, *flagInfluxBucket)
	} else {
		store = storage.NewMemoryStore()
		log.Printf("collector: using in-memory store")
	}

	conn, err := grpc.Dial(*flagBroker, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial broker: %w", err)
	}
	defer conn.Close()
	client := telemetryv1.NewTelemetryClient(conn)

	stream, err := client.Subscribe(ctx, &telemetryv1.SubscriptionRequest{Group: *flagGroup})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	return runCollectorLoop(ctx, stream, store, *flagBatchSize, *flagFlushMs, *flagWorkers)
}

type subscribeStream interface {
	Recv() (*telemetryv1.TelemetryData, error)
	Context() context.Context
}

var tickerFn = func(d time.Duration) *time.Ticker { return time.NewTicker(d) }

func runCollectorLoop(ctx context.Context, stream subscribeStream, store storage.Store, batchSize, flushMs, workers int) error {
	type job struct{ items []model.Telemetry }
	jobs := make(chan job, 64)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range jobs {
				start := time.Now()
				n := 0
				for _, it := range j.items {
					if err := store.SaveTelemetry(it); err != nil {
						metricFlushErrors.Inc()
						log.Printf("collector: flush error gpu=%s ts=%s: %v", it.GPUId, it.Timestamp.UTC().Format(time.RFC3339), err)
					} else {
						metricFlushed.Inc()
						n++
					}
				}
				dur := time.Since(start)
				metricFlushLatency.Observe(dur.Seconds())
				log.Printf("collector: worker=%d flushed=%d in %s", id, n, dur)
			}
		}(i)
	}

	ticker := tickerFn(time.Duration(flushMs) * time.Millisecond)
	defer ticker.Stop()

	batch := make([]model.Telemetry, 0, batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		copyBatch := make([]model.Telemetry, len(batch))
		copy(copyBatch, batch)
		batch = batch[:0]
		metricBacklog.Set(0)
		select {
		case jobs <- job{items: copyBatch}:
		default:
			jobs <- job{items: copyBatch}
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			close(jobs)
			waitDone := make(chan struct{})
			go func() { wg.Wait(); close(waitDone) }()
			select {
			case <-waitDone:
				return nil
			case <-time.After(time.Duration(*flagShutdownMs) * time.Millisecond):
				log.Printf("collector: shutdown timeout after %dms; exiting now", *flagShutdownMs)
				return nil
			}
		case <-ticker.C:
			log.Printf("collector: timer flush batch=%d", len(batch))
			flush()
		default:
			msg, err := stream.Recv()
			if err != nil {
				flush()
				close(jobs)
				waitDone := make(chan struct{})
				go func() { wg.Wait(); close(waitDone) }()
				select {
				case <-waitDone:
					return fmt.Errorf("recv: %w", err)
				case <-time.After(time.Duration(*flagShutdownMs) * time.Millisecond):
					log.Printf("collector: shutdown timeout after %dms; exiting now", *flagShutdownMs)
					return fmt.Errorf("recv: %w", err)
				}
			}
			metricReceived.Inc()
			if ok := validate(msg); !ok {
				metricDroppedInvalid.Inc()
				continue
			}
			t := toModel(msg)
			batch = append(batch, t)
			metricBatched.Inc()
			metricBacklog.Set(float64(len(batch)))
			if len(batch) >= batchSize {
				log.Printf("collector: size flush batch=%d", len(batch))
				flush()
			}
		}
	}
}

func validate(m *telemetryv1.TelemetryData) bool {
	if m == nil {
		return false
	}
	if stringsTrim(m.GetGpuId()) == "" {
		return false
	}
	if m.GetTs() == nil {
		return false
	}
	return true
}

func stringsTrim(s string) string { return strings.TrimSpace(s) }

func toModel(m *telemetryv1.TelemetryData) model.Telemetry {
	out := model.Telemetry{
		GPUId:     m.GetGpuId(),
		Timestamp: m.GetTs().AsTime(),
		Metrics:   map[string]float64{},
	}
	for k, v := range m.GetMetrics() {
		out.Metrics[k] = v
	}
	return out
}
