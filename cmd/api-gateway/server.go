package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"gpu-metric-collector/internal/storage"
)

// newServer builds an http.Handler with all routes, for testing and for main().
func newServer(store storage.Store) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/v1/gpus", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gpus, err := store.ListGPUs()
		if err != nil {
			log.Printf("api: list gpus error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, gpus)
	})

	mux.HandleFunc("/api/v1/gpus/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/v1/gpus/")
		parts := strings.Split(p, "/")
		if len(parts) != 2 || parts[1] != "telemetry" || parts[0] == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gpuID := parts[0]

		var startPtr, endPtr *time.Time
		if s := r.URL.Query().Get("start_time"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				http.Error(w, "invalid start_time", http.StatusBadRequest)
				return
			}
			startPtr = &t
		}
		if s := r.URL.Query().Get("end_time"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				http.Error(w, "invalid end_time", http.StatusBadRequest)
				return
			}
			endPtr = &t
		}

		items, err := store.QueryTelemetry(gpuID, startPtr, endPtr)
		if err != nil {
			log.Printf("api: query telemetry error gpu=%s start=%v end=%v: %v", gpuID, startPtr, endPtr, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, items)
	})

	// mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
	// 	http.ServeFile(w, r, "api/openapi.json")
	// })

	// Serve OpenAPI spec from absolute path baked into the image and provide an alias.
	if _, err := os.Stat("/api/openapi.json"); err != nil {
		log.Printf("api: warning: openapi spec not found at /api/openapi.json: %v", err)
	} else {
		log.Printf("api: openapi spec found at /api/openapi.json")
	}
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "/api/openapi.json")
	})
	mux.HandleFunc("/api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "/api/openapi.json")
	})

	// Simple Swagger UI via CDN at /docs
	mux.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>GPU Telemetry API Docs</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
      window.onload = () => { window.ui = SwaggerUIBundle({ url: '/openapi.json', dom_id: '#swagger-ui' }); };
    </script>
  </body>
</html>`))
	})

	// Serve static Swagger UI if generated at /api/swagger
	mux.Handle("/swagger/", http.StripPrefix("/swagger/", http.FileServer(http.Dir("/api/swagger"))))

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
