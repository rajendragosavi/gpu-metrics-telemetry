# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
ENV GOTOOLCHAIN=local
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/streamer ./cmd/streamer

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/streamer /streamer
COPY cmd/streamer/dcgm_metrics_20250718_134233.csv /data/dcgm.csv
USER nonroot
ENTRYPOINT ["/streamer"]
