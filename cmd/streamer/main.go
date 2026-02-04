package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	telemetryv1 "gpu-metric-collector/api/gen"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	flagCSV       = flag.String("csv", "dcgm_metrics_20250718_134233.csv", "Path to telemetry CSV file")
	flagBroker    = flag.String("broker", "127.0.0.1:9000", "Broker gRPC address")
	flagBatchSize = flag.Int("batch", 50, "Batch size for publish")
	flagTickMs    = flag.Int("tick_ms", 500, "Flush interval in ms")
	flagMetrics   = flag.String("metrics_addr", ":9101", "Metrics HTTP listen address")
	flagProducer  = flag.String("producer_id", "streamer-1", "Producer ID")
	flagHost      = flag.String("host_id", "", "Override host ID (default: os.Hostname)")
)

var (
	metricIngested = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "streamer", Name: "rows_ingested_total", Help: "CSV rows read.",
	})
	metricPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "streamer", Name: "items_published_total", Help: "Telemetry items published.",
	})
	metricBackpressure = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "streamer", Name: "backpressure_total", Help: "Backpressure responses from broker.",
	})
	metricErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "gpu_telemetry", Subsystem: "streamer", Name: "errors_total", Help: "Errors encountered.",
	})
	metricPublishLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "gpu_telemetry", Subsystem: "streamer", Name: "publish_latency_seconds", Help: "Latency of PublishBatch calls.",
		Buckets: prometheus.DefBuckets,
	})
	metricBatchPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "gpu_telemetry", Subsystem: "streamer", Name: "batch_pending", Help: "Current items buffered before publish.",
	})
)

func init() {
	prometheus.MustRegister(metricIngested, metricPublished, metricBackpressure, metricErrors, metricPublishLatency, metricBatchPending)
}

func main() {
	flag.Parse()

	hostname := *flagHost
	if hostname == "" {
		if h, err := os.Hostname(); err == nil {
			hostname = h
		} else {
			hostname = "unknown-host"
		}
	}

	http.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Printf("streamer: metrics on %s", *flagMetrics)
		_ = http.ListenAndServe(*flagMetrics, nil)
	}()

	conn, err := grpc.Dial(*flagBroker, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	client := telemetryv1.NewTelemetryClient(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("streamer: received shutdown signal, flushing...")
		cancel()
	}()

	if err := runStreamer(ctx, client, hostname, *flagProducer, *flagCSV, *flagBatchSize, time.Duration(*flagTickMs)*time.Millisecond); err != nil {
		log.Fatalf("streamer error: %v", err)
	}
}

func runStreamer(ctx context.Context, client telemetryv1.TelemetryClient, hostID, producerID, csvPath string, batchSize int, tick time.Duration) error {
	file, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("open csv: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(bufio.NewReader(file))
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	for i := range headers {
		headers[i] = strings.TrimSpace(strings.ToLower(headers[i]))
	}

	var batch []*telemetryv1.TelemetryData
	flushTicker := time.NewTicker(tick)
	defer flushTicker.Stop()

	backoff := 100 * time.Millisecond
	const backoffMax = 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			if len(batch) > 0 {
				drainRemaining(context.Background(), client, batch, &backoff, backoffMax)
			}
			log.Printf("streamer: exiting")
			return nil
		case <-flushTicker.C:
			if len(batch) > 0 {
				log.Printf("streamer: timer flush batch=%d", len(batch))
				drainRemaining(ctx, client, batch, &backoff, backoffMax)
				batch = batch[:0]
				metricBatchPending.Set(0)
			}
		default:
			rec, err := reader.Read()
			if err != nil {
				if err == io.EOF {
					if _, err2 := file.Seek(0, 0); err2 != nil {
						return fmt.Errorf("seek: %w", err2)
					}
					reader = csv.NewReader(bufio.NewReader(file))
					reader.FieldsPerRecord = -1
					headers, err = reader.Read()
					if err != nil {
						return fmt.Errorf("re-read header: %w", err)
					}
					for i := range headers {
						headers[i] = strings.TrimSpace(strings.ToLower(headers[i]))
					}
					continue
				}
				return fmt.Errorf("csv read: %w", err)
			}
			metricIngested.Inc()
			item := toTelemetry(headers, rec, hostID, producerID)
			fmt.Printf("item - %+v \n", item)
			if item != nil && item.GpuId != "" && item.GpuId != "gpu-unknown" {
				batch = append(batch, item)
			}
			metricBatchPending.Set(float64(len(batch)))
			if len(batch) >= batchSize {
				log.Printf("streamer: size flush batch=%d", len(batch))
				drainRemaining(ctx, client, batch, &backoff, backoffMax)
				batch = batch[:0]
				metricBatchPending.Set(0)
			}
		}
	}
}

// drainRemaining publishes remaining items with partial-accept and backpressure retry handling.
func drainRemaining(ctx context.Context, client telemetryv1.TelemetryClient, remaining []*telemetryv1.TelemetryData, backoff *time.Duration, backoffMax time.Duration) {
	for len(remaining) > 0 {
		// exit promptly if shutdown requested
		select {
		case <-ctx.Done():
			return
		default:
		}
		acc, bp, err := publishBatch(ctx, client, remaining)
		if err != nil {
			metricErrors.Inc()
			// if context canceled, exit without further retries
			if ctx.Err() != nil {
				return
			}
			log.Printf("streamer: publish error: %v (retrying in %s)", err, backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(*backoff):
			}
			if *backoff < backoffMax {
				*backoff *= 2
			}
			continue
		}
		if bp {
			if acc > 0 {
				remaining = remaining[acc:]
				log.Printf("streamer: backpressure accepted=%d remaining=%d", acc, len(remaining))
			} else {
				log.Printf("streamer: backpressure accepted=0 remaining=%d", len(remaining))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(*backoff):
			}
			if *backoff < backoffMax {
				*backoff *= 2
			}
			continue
		}
		// all accepted
		remaining = remaining[:0]
		*backoff = 100 * time.Millisecond
	}
}

// publishBatch returns (accepted, backpressure, err)
func publishBatch(ctx context.Context, client telemetryv1.TelemetryClient, batch []*telemetryv1.TelemetryData) (int, bool, error) {
	start := time.Now()
	resp, err := client.PublishBatch(ctx, &telemetryv1.TelemetryBatch{Items: batch})
	metricPublishLatency.Observe(time.Since(start).Seconds())
	if err != nil {
		return 0, false, err
	}
	accepted := int(resp.GetAccepted())
	metricPublished.Add(float64(accepted))
	if resp.GetStatus() == "BACKPRESSURE" {
		metricBackpressure.Inc()
		return accepted, true, nil
	}
	log.Printf("streamer: published ok accepted=%d", accepted)
	return accepted, false, nil
}

func toTelemetry(headers, rec []string, hostID, producerID string) *telemetryv1.TelemetryData {
	gpuID := ""
	metrics := make(map[string]float64)
	// detect a metric-name column common in DCGM/Influx exports
	fieldNameIdx := -1
	for i, h2 := range headers {
		switch h2 {
		case "_field", "field_name", "metric_name", "metric", "name":
			fieldNameIdx = i
		}
	}
	for i, h := range headers {
		if i >= len(rec) {
			continue
		}
		val := strings.TrimSpace(rec[i])
		switch h {
		case "gpu", "gpu_id", "gpuuuid", "gpu_uuid":
			gpuID = val
			continue
		case "host", "host_id", "hostname":
			continue
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			// If numeric column is generic and we have a metric-name column, use that as key
			if (h == "value" || h == "_value") && fieldNameIdx >= 0 && fieldNameIdx < len(rec) {
				key := strings.TrimSpace(rec[fieldNameIdx])
				key = strings.ToLower(key)
				if key != "" {
					metrics[key] = f
					continue
				}
			}
			metrics[h] = f
		}
	}
	if gpuID == "" {
		return nil
	}
	return &telemetryv1.TelemetryData{
		ProducerId: producerID,
		HostId:     hostID,
		GpuId:      gpuID,
		Ts:         timestamppb.Now(),
		Metrics:    metrics,
	}
}
