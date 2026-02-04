SHELL := /bin/bash

.PHONY: build test cover cover-pkg cover-html openapi-gen tidy proto-gen proto-tools

build:
	go build ./...

test:
	go test ./... -coverprofile=coverage.out -covermode=atomic

cover: test
	go tool cover -func=coverage.out

# Per-package coverage summary (quick view)
cover-pkg:
	go test -cover ./...

# Generate HTML coverage report from coverage.out
cover-html: test
	go tool cover -html=coverage.out -o coverage.html

SWAGGER_DIR := api/swagger
SWAGGER_CSS := $(SWAGGER_DIR)/swagger-ui.css
SWAGGER_BUNDLE := $(SWAGGER_DIR)/swagger-ui-bundle.js



# InfluxDB subchart toggle and bootstrap parameters (override via make vars)
ENABLE_INFLUXDB ?= 1
INFLUX_USER ?= admin
INFLUX_PASSWORD ?= StrongPass123!
INFLUX_TOKEN ?= ebcYqpdgAwaGFmTcoYAeeqG05oPsOzumHFSoBvot9Yib5s8enmJu4Tmd6mxJ4KHPF8w-LsGPx52rPdbTSf_OZw==
INFLUX_ORG ?= ai_cluster
INFLUX_BUCKET ?= telemetry

ifeq ($(ENABLE_INFLUXDB),1)
HELM_INFLUX_FLAGS := \
  --set influxdb2.enabled=true \
  --set influxdb2.adminUser.user=$(INFLUX_USER) \
  --set influxdb2.adminUser.password='$(INFLUX_PASSWORD)' \
  --set influxdb2.adminUser.token='$(INFLUX_TOKEN)' \
  --set influxdb2.adminUser.organization='$(INFLUX_ORG)' \
  --set influxdb2.adminUser.bucket='$(INFLUX_BUCKET)'
endif

# Download a static Swagger UI bundle locally and generate index.html pointing to /openapi.json
.PHONY: swagger-static
swagger-static:
	mkdir -p $(SWAGGER_DIR)
	curl -fsSL -o $(SWAGGER_CSS) https://unpkg.com/swagger-ui-dist@5/swagger-ui.css
	curl -fsSL -o $(SWAGGER_BUNDLE) https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js
	printf '%s\n' \
		'<!doctype html>' \
		'<html>' \
		'  <head>' \
		'    <meta charset="utf-8" />' \
		'    <title>GPU Telemetry API Docs</title>' \
		'    <link rel="stylesheet" href="/swagger/swagger-ui.css" />' \
		'  </head>' \
		'  <body>' \
		'    <div id="swagger-ui"></div>' \
		'    <script src="/swagger/swagger-ui-bundle.js"></script>' \
		'    <script>' \
		"      window.onload = () => {" \
		"        window.ui = SwaggerUIBundle({ url: '/openapi.json', dom_id: '#swagger-ui' });" \
		"      };" \
		'    </script>' \
		'  </body>' \
		'</html>' \
		> $(SWAGGER_DIR)/index.html

.PHONY: swagger-clean
swagger-clean:
	rm -rf $(SWAGGER_DIR)

openapi-gen:
	@echo "[stub] generate OpenAPI spec here (e.g., using oapi-codegen or swag)"

tidy:
	go mod tidy

# ---- KIND + Helm deploy ----
.PHONY: kind-up kind-delete docker-build kind-load helm-add-repos helm-install-monitoring helm-install helm-uninstall port-forward

KIND_CLUSTER ?= kind-gpu-telemetry
NAMESPACE ?= gpu-telemetry
IMG_TAG ?= dev

docker-build:
	docker build -t broker:$(IMG_TAG) -f cmd/mq-broker/Dockerfile .
	docker build -t collector:$(IMG_TAG) -f cmd/collector/Dockerfile .
	docker build -t streamer:$(IMG_TAG) -f cmd/streamer/Dockerfile .
	docker build -t api-gateway:$(IMG_TAG) -f cmd/api-gateway/Dockerfile .

kind-up:
	kind create cluster --name $(KIND_CLUSTER) || true

kind-delete:
	kind delete cluster --name $(KIND_CLUSTER) || true

kind-load:
	kind load docker-image broker:$(IMG_TAG) --name $(KIND_CLUSTER) 
	kind load docker-image collector:$(IMG_TAG) --name $(KIND_CLUSTER) 
	kind load docker-image streamer:$(IMG_TAG) --name $(KIND_CLUSTER) 
	kind load docker-image api-gateway:$(IMG_TAG) --name $(KIND_CLUSTER) 

helm-add-repos:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
	helm repo add influxdata https://helm.influxdata.com/ || true
	helm repo add bitnami https://charts.bitnami.com/bitnami || true
	helm repo update

helm-install-monitoring:
	helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack -n monitoring --create-namespace
	helm repo add influxdata https://helm.influxdata.com/ || true

helm-install:
	# Ensure subchart dependencies (e.g., influxdb2) are pulled before install
	helm dependency update ./deploy/charts/gpu-telemetry
	helm upgrade --install gpu-telemetry ./deploy/charts/gpu-telemetry -n $(NAMESPACE) --create-namespace \
		--set image.tag=$(IMG_TAG) $(HELM_INFLUX_FLAGS)

helm-uninstall:
	helm uninstall gpu-telemetry -n $(NAMESPACE) || true

port-forward:
	kubectl -n $(NAMESPACE) port-forward svc/api-gateway 8080:8080

# One-shot: build, load, install monitoring and app
.PHONY: kind-deploy
kind-deploy: docker-build kind-up kind-load helm-add-repos helm-install-monitoring helm-install

# --- Protobuf / gRPC codegen ---
# Requires: protoc, protoc-gen-go, protoc-gen-go-grpc installed and on PATH
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

PROTO_DIR := api/proto
GEN_OUT := api/gen

proto-gen:
	mkdir -p $(GEN_OUT)
	protoc -I $(PROTO_DIR) \
		--go_out=$(GEN_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_OUT) --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/telemetry.proto

proto-tools:
	@echo "Installing protoc plugins..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# ---- Independent image builds ----
.PHONY: docker-build-broker docker-build-collector docker-build-streamer docker-build-api

docker-build-broker:
	docker build -t broker:$(IMG_TAG) -f cmd/mq-broker/Dockerfile .

docker-build-collector:
	docker build -t collector:$(IMG_TAG) -f cmd/collector/Dockerfile .

docker-build-streamer:
	docker build -t streamer:$(IMG_TAG) -f cmd/streamer/Dockerfile .

docker-build-api:
	docker build -t api-gateway:$(IMG_TAG) -f cmd/api-gateway/Dockerfile .

# ---- Independent image loads into KIND ----
.PHONY: kind-load-broker kind-load-collector kind-load-streamer kind-load-api

kind-load-broker:
	kind load docker-image broker:$(IMG_TAG) --name $(KIND_CLUSTER)

kind-load-collector:
	kind load docker-image collector:$(IMG_TAG) --name $(KIND_CLUSTER) 

kind-load-streamer:
	kind load docker-image streamer:$(IMG_TAG) --name $(KIND_CLUSTER) 

kind-load-api:
	kind load docker-image api-gateway:$(IMG_TAG) --name $(KIND_CLUSTER) 

# ---- Convenience: build+load single component ----
.PHONY: bl-broker bl-collector bl-streamer bl-api

bl-broker: docker-build-broker kind-load-broker
bl-collector: docker-build-collector kind-load-collector
bl-streamer: docker-build-streamer kind-load-streamer
bl-api: docker-build-api kind-load-api
