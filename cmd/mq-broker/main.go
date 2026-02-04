package main

import (
    "flag"
    "fmt"
    "log"
    "net"
    "net/http"

    "google.golang.org/grpc"
    health "google.golang.org/grpc/health"
    healthpb "google.golang.org/grpc/health/grpc_health_v1"

    "github.com/prometheus/client_golang/prometheus/promhttp"

    telemetryv1 "gpu-metric-collector/api/gen"
    "gpu-metric-collector/internal/broker"
)

var (
    flagGRPC    = flag.String("grpc_addr", ":9000", "Broker gRPC listen addr")
    flagMetrics = flag.String("metrics_addr", ":9001", "Broker metrics listen addr")
    flagQCap    = flag.Int("queue_cap", 10000, "Inbound queue capacity")
    flagSBuf    = flag.Int("sub_buf", 256, "Per-subscriber buffer")
)

func main() {
    flag.Parse()
    addr := *flagGRPC
    lis, err := net.Listen("tcp", addr)
    if err != nil {
        log.Fatalf("listen: %v", err)
    }

    grpcServer := grpc.NewServer()

    // health service
    h := health.NewServer()
    healthpb.RegisterHealthServer(grpcServer, h)

    // telemetry broker
    telemetryv1.RegisterTelemetryServer(grpcServer, broker.NewServer(*flagQCap, *flagSBuf))

    // metrics server
    http.Handle("/metrics", promhttp.Handler())
    go func() {
        maddr := *flagMetrics
        fmt.Printf("mq-broker: metrics on %s\n", maddr)
        if err := http.ListenAndServe(maddr, nil); err != nil {
            log.Printf("metrics serve error: %v", err)
        }
    }()

    fmt.Printf("mq-broker: gRPC listening on %s\n", addr)
    if err := grpcServer.Serve(lis); err != nil {
        log.Fatalf("serve: %v", err)
    }
}
