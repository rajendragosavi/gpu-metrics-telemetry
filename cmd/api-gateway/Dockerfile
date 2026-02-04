FROM golang:1.24-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
RUN apk add --no-cache git ca-certificates
ENV GOTOOLCHAIN=local
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/api-gateway ./cmd/api-gateway

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/api-gateway /api-gateway
COPY --from=build /src/api /api
USER nonroot
ENTRYPOINT ["/api-gateway"]
