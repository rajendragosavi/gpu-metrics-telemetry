# Metrics Reference

This document summarizes the Prometheus metrics exposed by each component and how to use them to measure throughput, backpressure, and latency.

## Streamer

- Counters
  - `gpu_telemetry_streamer_rows_ingested_total`
  - `gpu_telemetry_streamer_items_published_total`
  - `gpu_telemetry_streamer_backpressure_total`
  - `gpu_telemetry_streamer_errors_total`
- Histograms
  - `gpu_telemetry_streamer_publish_latency_seconds`
- Gauges
  - `gpu_telemetry_streamer_batch_pending`

- Throughput (items/sec)
  - `rate(gpu_telemetry_streamer_items_published_total[1m])`
- Backpressure rate (events/sec)
  - `rate(gpu_telemetry_streamer_backpressure_total[1m])`
- Publish p95 latency
  - `histogram_quantile(0.95, rate(gpu_telemetry_streamer_publish_latency_seconds_bucket[5m]))`
- Quick checks
  - `curl -s http://<streamer-host>:9101/metrics | egrep 'items_published_total|backpressure_total'`

## Broker (Queue)

- Counters
  - `gpu_telemetry_broker_messages_enqueued_total`
  - `gpu_telemetry_broker_messages_delivered_total`
  - `gpu_telemetry_broker_backpressure_events_total`
  - `gpu_telemetry_broker_messages_requeued_total`
- Gauges
  - `gpu_telemetry_broker_subscribers`
  - `gpu_telemetry_broker_queue_depth`

- Ingress vs Egress rate (items/sec)
  - Ingress: `rate(gpu_telemetry_broker_messages_enqueued_total[1m])`
  - Egress: `rate(gpu_telemetry_broker_messages_delivered_total[1m])`
- Backpressure (events/sec)
  - `rate(gpu_telemetry_broker_backpressure_events_total[1m])`
- Queue depth monitoring
  - `gpu_telemetry_broker_queue_depth` (track saturation relative to capacity)
- Quick checks
  - `curl -s http://<broker-host>:9001/metrics | egrep 'messages_enqueued_total|messages_delivered_total|backpressure_events_total|queue_depth'`

## Collector

- Counters
  - `gpu_telemetry_collector_messages_received_total`
  - `gpu_telemetry_collector_messages_batched_total`
  - `gpu_telemetry_collector_messages_flushed_total`
  - `gpu_telemetry_collector_messages_dropped_invalid_total`
  - `gpu_telemetry_collector_flush_errors_total`
- Gauges
  - `gpu_telemetry_collector_backlog`
- Histograms
  - `gpu_telemetry_collector_flush_latency_seconds`

- Processing rate (items/sec)
  - Receive: `rate(gpu_telemetry_collector_messages_received_total[1m])`
  - Flushed: `rate(gpu_telemetry_collector_messages_flushed_total[1m])`
- Flush p95 latency
  - `histogram_quantile(0.95, rate(gpu_telemetry_collector_flush_latency_seconds_bucket[5m]))`
- Backlog level
  - `gpu_telemetry_collector_backlog`
- Quick checks
  - `curl -s http://<collector-host>:9102/metrics | egrep 'messages_(received|flushed)_total|flush_latency_seconds'`

## End-to-End Interpretation

- If `enqueued_rate > delivered_rate` for sustained periods and `queue_depth` rises, collectors are the bottleneck.
- If streamer `backpressure_total` climbs while `subscribers` is low, add collectors.
- Tune levers:
  - Streamer: `-batch`, `-tick_ms`
  - Broker: `-queue_cap`, `-sub_buf`
  - Collector: `-workers`, `-batch`, `-flush_ms`

## Suggested Grafana Panels

- Streamer publish rate and p95 publish latency
- Broker enqueue vs deliver rate and queue depth
- Collector flushed rate and p95 flush latency
- Backpressure rates (streamer and broker)
