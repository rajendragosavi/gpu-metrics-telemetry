package broker

import (
    "context"
    "errors"
    "log"
    "sync"
    "time"

    telemetryv1 "gpu-metric-collector/api/gen"

    "github.com/prometheus/client_golang/prometheus"
)

type subscriber struct {
    id string
    ch chan *telemetryv1.TelemetryData
}

type Server struct {
    telemetryv1.UnimplementedTelemetryServer

    mu       sync.Mutex
    subs     []*subscriber
    next     int
    inbound  chan *telemetryv1.TelemetryData
    queueCap int
    subBuf   int
}

var (
    metricEnqueued = prometheus.NewCounter(prometheus.CounterOpts{
        Namespace: "gpu_telemetry",
        Subsystem: "broker",
        Name:      "messages_enqueued_total",
        Help:      "Total messages accepted into the broker queue.",
    })
    metricDelivered = prometheus.NewCounter(prometheus.CounterOpts{
        Namespace: "gpu_telemetry",
        Subsystem: "broker",
        Name:      "messages_delivered_total",
        Help:      "Total messages delivered to subscribers.",
    })
    metricBackpressure = prometheus.NewCounter(prometheus.CounterOpts{
        Namespace: "gpu_telemetry",
        Subsystem: "broker",
        Name:      "backpressure_events_total",
        Help:      "Total backpressure events when queue was full.",
    })
    metricRequeued = prometheus.NewCounter(prometheus.CounterOpts{
        Namespace: "gpu_telemetry",
        Subsystem: "broker",
        Name:      "messages_requeued_total",
        Help:      "Total messages requeued due to subscriber send errors.",
    })
    metricSubscribers = prometheus.NewGauge(prometheus.GaugeOpts{
        Namespace: "gpu_telemetry",
        Subsystem: "broker",
        Name:      "subscribers",
        Help:      "Current number of active subscribers.",
    })
    metricQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
        Namespace: "gpu_telemetry",
        Subsystem: "broker",
        Name:      "queue_depth",
        Help:      "Current depth of the inbound queue.",
    })
)

func init() {
    prometheus.MustRegister(metricEnqueued, metricDelivered, metricBackpressure, metricRequeued, metricSubscribers, metricQueueDepth)
}

func NewServer(queueCap, subBuf int) *Server {
    s := &Server{
        inbound:  make(chan *telemetryv1.TelemetryData, queueCap),
        queueCap: queueCap,
        subBuf:   subBuf,
    }
    go s.dispatcher()
    // queue depth sampler
    go func() {
        ticker := time.NewTicker(200 * time.Millisecond)
        defer ticker.Stop()
        for range ticker.C {
            metricQueueDepth.Set(float64(len(s.inbound)))
        }
    }()
    return s
}

func (s *Server) PublishBatch(ctx context.Context, req *telemetryv1.TelemetryBatch) (*telemetryv1.PublishResponse, error) {
    if req == nil {
        return nil, errors.New("nil request")
    }
    accepted := 0
    for i := range req.Items {
        item := req.Items[i]
        select {
        case s.inbound <- item:
            accepted++
            metricEnqueued.Inc()
            if accepted%1000 == 0 {
                log.Printf("broker: enqueued accepted=%d", accepted)
            }
        default:
            metricBackpressure.Inc()
            log.Printf("broker: backpressure after accepted=%d depth=%d", accepted, len(s.inbound))
            return &telemetryv1.PublishResponse{Accepted: int64(accepted), Status: "BACKPRESSURE"}, nil
        }
    }
    return &telemetryv1.PublishResponse{Accepted: int64(accepted), Status: "OK"}, nil
}

func (s *Server) Subscribe(req *telemetryv1.SubscriptionRequest, stream telemetryv1.Telemetry_SubscribeServer) error {
    id := time.Now().UTC().Format("20060102T150405.000000000")
    sub := &subscriber{
        id: id,
        ch: make(chan *telemetryv1.TelemetryData, s.subBuf),
    }
    s.addSubscriber(sub)
    log.Printf("broker: subscriber added id=%s", id)
    defer s.removeSubscriber(sub.id)

    for {
        select {
        case <-stream.Context().Done():
            return nil
        case msg := <-sub.ch:
            if msg == nil {
                return nil
            }
            if err := stream.Send(msg); err != nil {
                // drop subscriber, re-enqueue the message
                s.removeSubscriber(sub.id)
                select {
                case s.inbound <- msg:
                    metricRequeued.Inc()
                    log.Printf("broker: requeued after send error")
                default:
                    // if queue is full, drop on floor to avoid deadlock
                }
                return err
            }
            metricDelivered.Inc()
        }
    }
}

func (s *Server) addSubscriber(sub *subscriber) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.subs = append(s.subs, sub)
    metricSubscribers.Set(float64(len(s.subs)))
}

func (s *Server) removeSubscriber(id string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    n := 0
    for _, sub := range s.subs {
        if sub.id != id {
            s.subs[n] = sub
            n++
        }
    }
    s.subs = s.subs[:n]
    metricSubscribers.Set(float64(len(s.subs)))
    log.Printf("broker: subscriber removed id=%s remain=%d", id, len(s.subs))
}

func (s *Server) snapshotSubs() []*subscriber {
    s.mu.Lock()
    defer s.mu.Unlock()
    out := make([]*subscriber, len(s.subs))
    copy(out, s.subs)
    return out
}

func (s *Server) dispatcher() {
    for msg := range s.inbound {
        for {
            subs := s.snapshotSubs()
            if len(subs) == 0 {
                // no subscribers yet; brief sleep and retry
                time.Sleep(5 * time.Millisecond)
                continue
            }
            delivered := false
            start := s.next
            for i := 0; i < len(subs); i++ {
                idx := (start + i) % len(subs)
                sel := subs[idx]
                select {
                case sel.ch <- msg:
                    // advance round-robin pointer
                    s.mu.Lock()
                    s.next = (idx + 1) % len(subs)
                    s.mu.Unlock()
                    delivered = true
                    break
                default:
                    // target is full, try next
                }
            }
            if delivered {
                break
            }
            // all subscriber queues are full; brief backoff
            time.Sleep(1 * time.Millisecond)
        }
    }
}
