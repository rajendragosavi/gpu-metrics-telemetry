# GPU Telemetry Pipeline

A lightweight, Kubernetes-first pipeline for ingesting GPU telemetry metrics. It reads metrics from a CSV file and streams them via a gRPC message-broker, persists them into InfluxDB, and exposes data through a simple REST API.
It has components like - Streamer, Broker, Collector, API Gateway and Influx db storage.


---

## Overview
- Streamer reads telemetry from a CSV baked into the container (`/data/dcgm.csv`), validates rows (skips missing `gpu_id`), batches, and publishes to the Broker.
- Broker is a simple in-memory gRPC queue with backpressure and per-subscriber buffers.
- Collector subscribes to the Broker, validates and batches messages, and writes to InfluxDB 2.x using org/bucket/token.
- API Gateway exposes REST endpoints to query telemetry (e.g., list GPUs, time-range queries).
- Prometheus scrapes component metrics; Grafana renders a ready-to-use dashboard.

For a detailed architecture, see:
- `architecture/docs/architecture.md`

---

## Prerequisites
- Linux/macOS with:
  - Docker (for building images)
  - KIND (Kubernetes-in-Docker)
  - kubectl (cluster management)
  - Helm (install charts)
  - Go 1.21+ (build/test locally)
- Network: ability to bind local ports for `kubectl port-forward`

---

## Build

- Git clone the repository.

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
- `make kind-deploy PLATFORM=linux/arm64 KIND_CLUSTER=<cluster-name> ENABLE_INFLUXDB=1`

Note: PLATFORM=linux/arm64 - for apple silicon


What this does:
- Builds images and loads them into the KIND cluster
- Installs kube-prometheus-stack in the `monitoring` namespace
- Installs the gpu-telemetry Helm chart in the `gpu-telemetry` namespace
- If `ENABLE_INFLUXDB=1` is set, it also enables and bootstraps the InfluxDB 2.x subchart using the provided admin credentials (user defaults to `admin`, org to `ai_cluster`, bucket to `telemetry`—overridable via make vars)


2) Verify if all components are running:
- `kubectl -n gpu-telemetry get pods`

3) Port-forward API GW and run curl command to verify /api/v1/gpus endpoint is working:
- `kubectl -n gpu-telemetry port-forward svc/api-gateway 8080:8080`

- `curl http://localhost:8080/api/v1/gpus`

- `curl http://localhost:8080/api/v1/gpus/1/telemetry`


4) If you want to tear down the setup
- `make helm-uninstall`

---

## Important Make commands

### Cluster lifecycle
- `make kind-up KIND_CLUSTER=<name>`
- `make kind-delete KIND_CLUSTER=<name>`

### Images
- `make docker-build` / `make docker-build-collector` (etc.)
  - `make kind-load KIND_CLUSTER=<name>` / `make kind-load-collector` (etc.)

### Monitoring
- `make helm-install-monitoring`

### App install
- `make helm-install ENABLE_INFLUXDB=1 INFLUX_PASSWORD='...' INFLUX_TOKEN='...'`
  - `make helm-uninstall`

### Port-forward API
  - `make port-forward`

### Tests and Coverage
- Run all tests with coverage profile:
  - `make test`
- Show coverage summary:
  - `make cover`
- Generate HTML coverage report:
  - `make cover-html` (opens `coverage.html`)
- Per-package quick coverage:
  - `make cover-pkg`

### OpenAPI and Swagger
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

Please run this commands to port forward the grafana dashboard:

`kubectl port-forward svc/kube-prometheus-stack-grafana -n monitoring 8484:80`

You will see a dasboard ```gpu-telemetry-pipeline``` in the grafana dashboard.


Following are some important metrics we are collecting to understand our system performance.

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


## How I used AI to build this?

I read the problem statement and I noted down early mind map of how the system will look like for example - the core of the system is message queue. Its a proper producer consumer. I had done something similar but very basic with just golang channels there was no persistence and concepts like back-pressure. 

I  think knowing some basics about queue and backpressure is important to understand this system. I used AI to understand this system and build it. I used Perplexity majorly to brainstorm around edge cases and scenarios to understand nuances of the system. 

Once I got some clarity like - I started noting components and how the component should behave. Like message schema, channel pattern.

I used windsurf to generate code for majorly - queue and collector part where I wanted to handle back-pressure and persistence. Idea of using influxDB came from Perplexity. 

Once system was in place, I started testing it and then added metrics - which is where again I used windsurf to generate code for metrics. 

I prompted exactly what I need to measure and then windsurf generated the code for me. I also brainstormed with Perplexity around what metrics can be useful to measure system health and performance. 

My mantra so far using Code Pilot/AI is  - 

1. First understand the system. Make mental model - put it on paper then brain storm as much as possible using AI tools to make sure we cover all the edge cases and scenarios.
2. Make a plan or steps of each components and then wherever needed use Perplexity/Windsurf or any AI IDEs to generate code wherever required.


I have been using this mantra for last 3 months or so - 
* I am finding it useful but I am still trying to use it cautiously as I do not want any critical changes to be done by AI where am less blind or less comfortable.
* For this exercise I used AI heavily as it was a greenfield system but in my current work - I am trying to use it more cautiously.



