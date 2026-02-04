package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gpu-metric-collector/internal/storage"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	influxURL := flag.String("influx_url", "", "InfluxDB URL, e.g. http://localhost:8086")
	influxOrg := flag.String("influx_org", "", "InfluxDB organization")
	influxBucket := flag.String("influx_bucket", "", "InfluxDB bucket")
	influxToken := flag.String("influx_token", "", "InfluxDB API token")
	flag.Parse()

	var store storage.Store
	if *influxURL != "" && *influxOrg != "" && *influxBucket != "" && *influxToken != "" {
		s, err := storage.NewInfluxStore(*influxURL, *influxOrg, *influxBucket, *influxToken)
		if err != nil {
			log.Fatalf("open influx store: %v", err)
		}
		store = s
		log.Printf("api-gateway: using influx store url=%s org=%s bucket=%s", *influxURL, *influxOrg, *influxBucket)
	} else {
		store = storage.NewMemoryStore()
		log.Printf("api-gateway: using in-memory store")
	}

	handler := newServer(store)
	server := &http.Server{Addr: *addr, Handler: handler}

	// graceful shutdown
	go func() {
		log.Printf("api-gateway: listening on %s with /api/v1 endpoints", *addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
