# Architecture Overview

This system ingests GPU telemetry from CSV via Streamers, routes it through a custom gRPC message broker, and persists it via Collectors. An API Gateway exposes read-only endpoints.

- Components
  - Broker (gRPC): in-memory work-queue, round-robin dispatch, backpressure.
  - Streamer: reads CSV, batches, publishes to Broker.
  - Collector: subscribes from Broker, validates, batches, writes to storage.
  - Storage: in-memory first; optional SQLite later.
  - API Gateway: REST to list GPUs and query telemetry.
- Scale and resilience
  - Horizontal scaling for Streamers and Collectors.
  - Backpressure signaled from Broker, exponential backoff at Streamer.
  - Requeue on subscriber errors; health and metrics for observability.
