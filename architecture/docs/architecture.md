# GPU Telemetry Pipeline – Architecture and Design

This document explains our distributed message queue system, what each part does, why it exists, and how data flows from raw CSV to dashboards and APIs. 

## System Overview
- We have GPUs producing metrics (for now, from a CSV export). 
- A Streamer reads that CSV and turns rows into telemetry messages.
- A lightweight Broker accepts those messages over gRPC and fans them out.
- A Collector subscribes, validates, batches, and writes them to InfluxDB 2.x.
- An API Gateway provides simple REST endpoints to query what’s stored.
- Prometheus and Grafana watch the whole pipeline so you can see if it’s healthy.

Open the diagram for a quick visual:
- architecture/docs/architecture.mermaid (Mermaid graph)




## Components (What they are and why they exist)

### Streamer (Ingestion)
- Reads telemetry from a CSV baked into the container at `/data/dcgm.csv`.
- Converts each row to a message: it must include a `gpu_id`; rows missing it are skipped.
- Handles “metric name in one column, numeric value in another” (e.g., `_field` + `_value`).
- Publishes batches to the Broker to smooth bursts and reduce chattiness.
- Why it exists: decouple the data source from the rest of the system and provide controlled, backpressured input.

### Broker (Transport)
- A simple gRPC-based, in-memory message hub.
- Streamer publishes batches; Collectors subscribe as a work queue.
- Headless Service for stable DNS and easier client resolution.
- Why it exists: isolate producers from consumers, absorb small spikes, and provide a clear handoff point with metrics.

### Collector (Persistence)
- Subscribes to the Broker stream, validates messages, and drops malformed ones.
- Batches by size/time and writes to InfluxDB 2.x (HTTP 8086) using org/bucket/token.
- Worker pool for concurrent writes; circuit-breaker semantics when storage is unhealthy.
- Graceful termination drains its backlog and does a final flush.
- Why it exists: provide a robust, controlled path from transient messages to durable timeseries storage.

### API Gateway (Access)
- REST endpoints:
  - `GET /api/v1/gpus` – list known GPU IDs.
  - `GET /api/v1/gpus/{id}/telemetry?start=...&end=...` – query telemetry over a window.
- Translates HTTP requests into Flux queries against InfluxDB and returns clean JSON.
- Why it exists: a simple, stable contract for UIs, scripts, and integrations.

### InfluxDB 2.x (Storage)
- Optional Helm subchart (enable it, or point to an external instance).
- Bootstrapped with admin credentials (Secret `influxdb2-auth`).
- Stores time-series telemetry; queried by the API and your dashboards.
- Why it exists: proven time-series database with a powerful query language (Flux).

### Prometheus & Grafana (Observability)
- Prometheus scrapes `/metrics` on Streamer, Broker, and Collector via ServiceMonitors.
- Grafana uses Prometheus as a datasource; a prebuilt dashboard is provisioned via ConfigMap.
- Why they exist: you should be able to see throughput, backpressure, errors, and latency at a glance.

## How Data Flows (A day in the life of a metric)
1. The Streamer reads a line from the CSV, checks that `gpu_id` is present, maps fields to metrics, and adds it to a batch.
2. The batch is sent to the Broker via gRPC. If the Broker signals backpressure, the Streamer adapts (smaller batches, waits between sends).
3. The Collector maintains a subscription to the Broker and pulls messages. It validates each, groups them, and writes to InfluxDB.
4. InfluxDB acknowledges writes. If it fails (network, auth, overload), the Collector logs errors, backs off, and trips readiness to let Kubernetes react.
5. When a client calls the API, the Gateway issues a Flux query to InfluxDB and returns the results as JSON.
6. Meanwhile, Prometheus scrapes metrics from all components; Grafana panels show health and trends.

## Design Considerations and Trade‑offs
- Simplicity first: an in-memory Broker is easy to operate. If you need guaranteed delivery or at-least-once semantics across outages, consider plugging in an external MQ (NATS/Kafka) later.
- CSV baked into the image: avoids ConfigMap size limits and PVC complexity. The trade‑off is you rebuild the image when the dataset changes. For dynamic sources, replace Streamer’s CSV reader with a live feed.
- Backpressure everywhere: the Broker won’t just drop on the floor; it signals pressure up to the Streamer. The Collector exposes readiness so Kubernetes can avoid sending it more work if storage is failing.
- Schema-lite telemetry: metrics are a `map[string]float64`. This makes it easy to ingest diverse fields but shifts stronger typing/validation to storage and consumers.
- Operational clarity: every component exports Prometheus metrics; there’s a dashboard ready to go. When things go wrong, you shouldn’t be blind.

## Configuration and Deployment (Helm)
- One Helm chart deploys Streamer, Broker, Collector, and API Gateway.
- Optional InfluxDB subchart (influxdata/influxdb2). Enable via values or Makefile toggle.
- Values of note:
  - `influxdb2.enabled`: set `true` to install InfluxDB in‑cluster.
  - `influxdb2.adminUser.*`: bootstrap user/password/token/org/bucket for the subchart.
  - `collector.influx.*` and `apiGateway.influx.*`: used when pointing to an external InfluxDB.
- Makefile convenience:
  - `make kind-deploy ENABLE_INFLUXDB=1 INFLUX_PASSWORD='...' INFLUX_TOKEN='...'` deploys to KIND with the subchart enabled.

## Observability – What to Watch
- Streamer
  - Published items, pending batch size, publish latency, backpressure count.
- Broker
  - Messages enqueued vs delivered, queue depth, backpressure events, subscriber count.
- Collector
  - Messages received/flushed, flush latency, backlog size, write errors.
- Golden signals
  - Rising backlog or queue depth → slow storage or insufficient collector workers.
  - Backpressure spikes → bursts from Streamer or bottleneck downstream.
  - Error rates on writes → credentials/URL/port/token mismatches or storage issues.

## Scaling & Tuning
- Scale Streamer for more ingestion (ensure sources differ to avoid duplicating data).
- Increase Collector workers and tune `batch` and `flush_ms` for write throughput.
- Broker buffer sizes (`queue_cap`, `sub_buf`) absorb bursts; watch memory.
- See architecture/docs/tuning.md for practical PromQL and starting values.

## Failure Scenarios (and what we do)
- InfluxDB unreachable → Collector retries, readiness fails, Broker queue may grow; watch queue depth and error logs.
- Bad data (missing `gpu_id`) → Streamer drops the row and logs/metrics reflect the discard.
- Pod restarts → Streamer and Collector drain and flush during termination; give them enough `terminationGracePeriodSeconds`.

## Security Notes
- Tokens and passwords live in K8s Secrets; Collector/API read via flags/env and should not log secrets.
- Network is internal (ClusterIP/Headless). Consider NetworkPolicies to restrict cross‑namespace access.
- Use hardened base images where possible; avoid shells in production images.

## Quick Start (for humans)
1. Build and load images into KIND:
   - `make docker-build kind-up kind-load`
2. Install monitoring:
   - `make helm-install-monitoring`
3. Install the app (with in‑cluster InfluxDB):
   - `make helm-install ENABLE_INFLUXDB=1 INFLUX_PASSWORD='...' INFLUX_TOKEN='...'`
4. Port‑forward API and try it:
   - `kubectl -n gpu-telemetry port-forward svc/api-gateway 8080:8080`
   - `curl http://localhost:8080/api/v1/gpus`

## Where to Look When Things Break
- Streamer logs: CSV parsing issues, skipped rows, backpressure.
- Broker metrics: queue depth and backpressure tell you who’s the bottleneck.
- Collector logs: Flux write errors usually mean URL/port/token/org/bucket problems.
- InfluxDB Service: must listen on `:8086` (HTTP). Verify the `influxdb2-auth` Secret.
- API Gateway logs: Flux query errors and time-range parsing.


