# How to Run (Local)

This guide explains how to start each component, what the flags mean, and how to observe metrics.

## 1) Message Broker (MQ)

Runs the gRPC broker that accepts telemetry from Streamers and dispatches to Collectors.

Command:

- `go run ./cmd/mq-broker -queue_cap 20000 -sub_buf 512`

Flags:
- `-grpc_addr` (default `:9000`): gRPC listen address for broker.
- `-metrics_addr` (default `:9001`): Prometheus metrics HTTP address.
- `-queue_cap` (default `10000`): Inbound queue capacity. Larger absorbs bursts.
- `-sub_buf` (default `256`): Per-subscriber (collector) buffer size.

Metrics: http://localhost:9001/metrics
- `gpu_telemetry_broker_messages_enqueued_total`
- `gpu_telemetry_broker_messages_delivered_total`
- `gpu_telemetry_broker_backpressure_events_total`
- `gpu_telemetry_broker_queue_depth`
- `gpu_telemetry_broker_subscribers`

## 2) Collector

Subscribes to the broker stream, validates messages, batches, and flushes to storage (in-memory for now).

Command:

- `go run ./cmd/collector -broker 127.0.0.1:9000 -workers 8 -batch 500 -flush_ms 200`

Flags:
- `-broker` (default `127.0.0.1:9000`): Broker gRPC address.
- `-group` (default `default`): Consumer group label (future use).
- `-workers` (default `4`): Flush worker goroutines. Increase for higher throughput.
- `-batch` (default `500`): Target batch size to flush to storage.
- `-flush_ms` (default `1000`): Max interval to force a flush if batch not full.
- `-metrics_addr` (default `:9102`): Prometheus metrics HTTP address.

Metrics: http://localhost:9102/metrics
- `gpu_telemetry_collector_messages_received_total`
- `gpu_telemetry_collector_messages_flushed_total`
- `gpu_telemetry_collector_flush_latency_seconds`
- `gpu_telemetry_collector_backlog`

## 3) Streamer

Reads CSV telemetry, batches, and publishes to the broker with backpressure handling.

Command:

- `go run ./cmd/streamer -csv dcgm_metrics_20250718_134233.csv -broker 127.0.0.1:9000 -batch 100 -tick_ms 300`

Flags:
- `-csv` (default `dcgm_metrics_20250718_134233.csv`): Path to CSV.
- `-broker` (default `127.0.0.1:9000`): Broker address.
- `-batch` (default `50`): Items per publish (larger is more efficient but burstier).
- `-tick_ms` (default `500`): Time-based flush interval.
- `-producer_id` (default `streamer-1`): Streamer identity string.
- `-host_id` (default OS hostname): Host identity override.
- `-metrics_addr` (default `:9101`): Prometheus metrics HTTP address.

Metrics: http://localhost:9101/metrics
- `gpu_telemetry_streamer_items_published_total`
- `gpu_telemetry_streamer_backpressure_total`
- `gpu_telemetry_streamer_publish_latency_seconds`
- `gpu_telemetry_streamer_batch_pending`

## Recommended Sequence

1. Start Broker:
   - `go run ./cmd/mq-broker -queue_cap 20000 -sub_buf 512`
2. Start one or more Collectors:
   - `go run ./cmd/collector -broker 127.0.0.1:9000 -workers 8 -batch 500 -flush_ms 200`
3. Start the Streamer:
   - `go run ./cmd/streamer -csv dcgm_metrics_20250718_134233.csv -broker 127.0.0.1:9000 -batch 100 -tick_ms 300`

## Tuning Tips

- If you see backpressure increasing:
  - Start more collectors or increase `-workers`.
  - Increase broker `-queue_cap` and `-sub_buf`.
  - Reduce streamer `-batch` or `-tick_ms` to smooth bursts.
- Watch `queue_depth` and keep it < 70% of capacity most of the time.
- Aim for publish p95 latency < 200ms and collector flush p95 < 250ms.

## 4) API Gateway (REST)

Serves read APIs to list GPUs and query telemetry, plus Op- `-producer_id` (default `streamer-1`): Streamer identity string.
enAPI/Swagger docs.

Command:

- `go run ./cmd/api-gateway`

Endpoints:
- Health: `GET http://localhost:8080/healthz`
- List GPUs: `GET http://localhost:8080/api/v1/gpus`
- Query Telemetry: `GET http://localhost:8080/api/v1/gpus/{id}/telemetry`
  - Optional query params (RFC3339): `start_time`, `end_time`

Docs:
- OpenAPI JSON: `http://localhost:8080/openapi.json`
- Swagger UI (CDN): `http://localhost:8080/docs`
- Swagger UI (static, if generated via Makefile): `http://localhost:8080/swagger/`
  - Generate bundle: `make swagger-static`
  - Clean bundle: `make swagger-clean`

Sample cURL:
- `curl -s http://localhost:8080/api/v1/gpus | jq`
- `curl -s "http://localhost:8080/api/v1/gpus/0/telemetry" | jq`
- `curl -s "http://localhost:8080/api/v1/gpus/0/telemetry?start_time=2026-01-26T00:00:00Z&end_time=2026-01-26T23:59:59Z" | jq`
