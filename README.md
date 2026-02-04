# GPU Telemetry Pipeline

A lightweight, Kubernetes-first pipeline for ingesting GPU telemetry, transporting it via a gRPC broker, persisting to InfluxDB 2.x, and exposing data through a simple REST API.
- Components: Streamer, Broker, Collector, API Gateway
- Storage: InfluxDB 2.x (optional Helm subchart or external)
- Deploy: Helm chart + KIND convenience targets
- Observe: kube-prometheus-stack + prebuilt Grafana dashboard

---

## Overview
- Streamer reads telemetry from a CSV baked into the container (`/data/dcgm.csv`), validates rows (skips missing `gpu_id`), batches, and publishes to the Broker.
- Broker is a simple in-memory gRPC queue with backpressure and per-subscriber buffers.
- Collector subscribes to the Broker, validates and batches messages, and writes to InfluxDB 2.x using org/bucket/token.
- API Gateway exposes REST endpoints to query telemetry (e.g., list GPUs, time-range queries).
- Prometheus scrapes component metrics; Grafana renders a ready-to-use dashboard.

For a visual, see:
- `architecture/docs/architecture.md` (human-friendly write-up)
- `architecture/docs/architecture.mermaid` (diagram)

---

## Prerequisites
- Linux/macOS with:
  - Docker (for building images)
  - KIND (Kubernetes-in-Docker)
  - kubectl (cluster management)
  - Helm (install charts)
  - Go 1.21+ (build/test locally)
- Optional (docs/diagrams):
  - Node + `@mermaid-js/mermaid-cli` or Docker for rendering Mermaid to SVG/PNG
- Network: ability to bind local ports for `kubectl port-forward`

---

## Build
- Build all component images locally:
  - `make docker-build`
- Build a single component:
  - `make docker-build-streamer` (or `-broker`, `-collector`, `-api`)

Load images into a KIND cluster (if already created):
- `make kind-load KIND_CLUSTER=<name>`
  - Or single component: `make kind-load-streamer KIND_CLUSTER=<name>`

---

## Deploy to KIND (one-shot)
The fastest way to bootstrap everything into a local KIND cluster:

1) Create/ensure cluster exists and build+load images, install monitoring, install app:
- `make kind-deploy KIND_CLUSTER=<clustername> ENABLE_INFLUXDB=1 INFLUX_PASSWORD='StrongPass123!' INFLUX_TOKEN='<YOUR_INFLUXDB_ADMIN_TOKEN>'`

What this does:
- Builds images and loads them into the KIND cluster
- Installs kube-prometheus-stack in the `monitoring` namespace
- Installs the gpu-telemetry Helm chart in the `gpu-telemetry` namespace
- If `ENABLE_INFLUXDB=1` is set, it also enables and bootstraps the InfluxDB 2.x subchart using the provided admin credentials (user defaults to `admin`, org to `ai_cluster`, bucket to `telemetry`—overridable via make vars)

Make variables you can override:
- `ENABLE_INFLUXDB` (0/1)
- `INFLUX_USER` (default `admin`)
- `INFLUX_PASSWORD` (required when enabled)
- `INFLUX_TOKEN` (required when enabled)
- `INFLUX_ORG` (default `ai_cluster`)
- `INFLUX_BUCKET` (default `telemetry`)
- `KIND_CLUSTER` (default `kind-gpu-telemetry`)
- `NAMESPACE` (default `gpu-telemetry`)
- `IMG_TAG` (default `dev`)

2) Verify components:
- `kubectl -n gpu-telemetry get pods`
- `helm get values -n gpu-telemetry gpu-telemetry -o yaml`

3) Port-forward API and try it:
- `kubectl -n gpu-telemetry port-forward svc/api-gateway 8080:8080`
- `curl http://localhost:8080/api/v1/gpus`

---

## Deploy via Helm manually
If you prefer running Helm commands directly:

1) Install monitoring (Prometheus/Grafana):
- `make helm-install-monitoring`

2) Pull subchart deps and install the app:
- `helm dependency update ./deploy/charts/gpu-telemetry`
- With in-cluster InfluxDB enabled:
```
helm upgrade --install gpu-telemetry ./deploy/charts/gpu-telemetry \
  -n gpu-telemetry --create-namespace \
  --set image.tag=dev \
  --set influxdb2.enabled=true \
  --set influxdb2.adminUser.user='admin' \
  --set influxdb2.adminUser.password='StrongPass123!' \
  --set influxdb2.adminUser.token='<YOUR_INFLUXDB_ADMIN_TOKEN>' \
  --set influxdb2.adminUser.organization='ai_cluster' \
  --set influxdb2.adminUser.bucket='telemetry'
```
- Or, with an external InfluxDB:
```
helm upgrade --install gpu-telemetry ./deploy/charts/gpu-telemetry \
  -n gpu-telemetry --create-namespace \
  --set image.tag=dev \
  --set influxdb2.enabled=false \
  --set collector.influx.url='http://YOUR_INFLUXDB:8086' \
  --set collector.influx.org='ai_cluster' \
  --set collector.influx.bucket='telemetry' \
  --set collector.influx.token='<YOUR_TOKEN>' \
  --set apiGateway.influx.url='http://YOUR_INFLUXDB:8086' \
  --set apiGateway.influx.org='ai_cluster' \
  --set apiGateway.influx.bucket='telemetry' \
  --set apiGateway.influx.token='<YOUR_TOKEN>'
```

Uninstall:
- `make helm-uninstall`

---

## Make commands (cheat sheet)
- Cluster lifecycle
  - `make kind-up KIND_CLUSTER=<name>`
  - `make kind-delete KIND_CLUSTER=<name>`
- Images
  - `make docker-build` / `make docker-build-collector` (etc.)
  - `make kind-load KIND_CLUSTER=<name>` / `make kind-load-collector` (etc.)
- Monitoring
  - `make helm-install-monitoring`
- App install
  - `make helm-install ENABLE_INFLUXDB=1 INFLUX_PASSWORD='...' INFLUX_TOKEN='...'`
  - `make helm-uninstall`
- Port-forward API
  - `make port-forward`

---

## Tests and Coverage
- Run all tests with coverage profile:
  - `make test`
- Show coverage summary:
  - `make cover`
- Generate HTML coverage report:
  - `make cover-html` (opens `coverage.html`)
- Per-package quick coverage:
  - `make cover-pkg`

---

## OpenAPI and Swagger
- The project serves a Swagger UI at `/swagger` (static files under `api/swagger` when generated).
- Generate static Swagger UI bundle locally (no server-side build required):
  - `make swagger-static`
  - This downloads a Swagger UI bundle and writes `api/swagger/index.html` pointing to `/openapi.json`.
- Clean the bundled UI:
  - `make swagger-clean`
- OpenAPI spec generation (stub target, hook up your generator as needed):
  - `make openapi-gen`

Once the API Gateway is running and port-forwarded:
- Swagger UI: `http://localhost:8080/swagger/`
- OpenAPI JSON: `http://localhost:8080/openapi.json`

---

## Grafana Dashboard (What to watch and why)

The chart provisions a ready-to-use dashboard (via ConfigMap) targeting Prometheus. Ensure kube-prometheus-stack is installed in the `monitoring` namespace and the dashboard sidecar picks it up. Each panel below helps you understand system health and throughput.

- **Streamer Throughput (msgs/sec)**
  - PromQL: `rate(gpu_telemetry_streamer_items_published_total[1m])`
  - Meaning: messages published to the Broker per second. A drop suggests upstream issues or backpressure.

- **Streamer Backpressure (events/sec)**
  - PromQL: `rate(gpu_telemetry_streamer_backpressure_total[1m])`
  - Meaning: frequency of backpressure hints from Broker. Persistent spikes indicate downstream saturation.

- **Streamer Batch Pending (items)**
  - PromQL: `gpu_telemetry_streamer_batch_pending`
  - Meaning: items currently buffered in the Streamer. Growing values imply publish delay or Broker slowness.

- **Broker Enqueued vs Delivered (msgs/sec)**
  - PromQL (enqueued): `rate(gpu_telemetry_broker_messages_enqueued_total[1m])`
  - PromQL (delivered): `rate(gpu_telemetry_broker_messages_delivered_total[1m])`
  - Meaning: ingress vs egress of the Broker. Delivered should roughly match enqueued over time. Gaps imply consumer lag.

- **Broker Queue Depth (items)**
  - PromQL: `gpu_telemetry_broker_queue_depth`
  - Meaning: in-flight backlog within the Broker. Sustained growth = downstream bottleneck (Collector/InfluxDB).

- **Broker Subscribers (count)**
  - PromQL: `gpu_telemetry_broker_subscribers`
  - Meaning: active Collector subscriptions. Helps confirm expected consumers are connected.

- **Collector Receive/Flush Throughput (msgs/sec)**
  - PromQL (received): `rate(gpu_telemetry_collector_messages_received_total[1m])`
  - PromQL (flushed): `rate(gpu_telemetry_collector_messages_flushed_total[1m])`
  - Meaning: how fast the Collector ingests and persists messages. Flushed should track received; divergence indicates write pressure.

- **Collector Backlog (items)**
  - PromQL: `gpu_telemetry_collector_backlog`
  - Meaning: pending items awaiting flush. Rising backlog = increase workers/batch size or check InfluxDB health.

- **Collector Flush Latency p95 (s)**
  - PromQL: `histogram_quantile(0.95, rate(gpu_telemetry_collector_flush_latency_seconds_bucket[5m]))`
  - Meaning: tail latency of flush operations. Jumps indicate storage stress or network issues.

- **End-to-end Health Hints**
  - High Streamer backpressure + rising Broker queue depth = consumers slow.
  - Collector received >> flushed + high flush latency = storage throttled or under-provisioned.
  - Flat/zero throughput across panels = upstream data stopped or connectivity broken.


