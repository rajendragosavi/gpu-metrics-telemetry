# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
RUN apk add --no-cache git ca-certificates
ENV GOTOOLCHAIN=local
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/mq-broker ./cmd/mq-broker

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mq-broker /mq-broker
USER nonroot
ENTRYPOINT ["/mq-broker"]
