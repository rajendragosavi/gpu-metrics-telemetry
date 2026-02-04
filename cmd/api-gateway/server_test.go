package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gpu-metric-collector/internal/model"
)

// fakeStore implements storage.Store for handler tests
type fakeStore struct {
	gpus    []string
	tel     map[string][]model.Telemetry
	saveErr error
}

func (f *fakeStore) SaveTelemetry(t model.Telemetry) error { return f.saveErr }
func (f *fakeStore) ListGPUs() ([]string, error)           { return f.gpus, nil }
func (f *fakeStore) QueryTelemetry(gpuID string, start, end *time.Time) ([]model.Telemetry, error) {
	items := f.tel[gpuID]
	// filter by window inclusively if provided
	var out []model.Telemetry
	for _, it := range items {
		if start != nil && it.Timestamp.Before(*start) {
			continue
		}
		if end != nil && it.Timestamp.After(*end) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func TestListGPUs_OK(t *testing.T) {
	fs := &fakeStore{gpus: []string{"gpu-1", "gpu-2"}}
	srv := newServer(fs)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got []string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(got) != 2 || got[0] != "gpu-1" || got[1] != "gpu-2" {
		t.Fatalf("unexpected list: %#v", got)
	}
}

func TestQueryTelemetry_OK_WithWindow(t *testing.T) {
	// Prepare telemetry across times
	base := time.Date(2026, 1, 26, 12, 0, 0, 0, time.UTC)
	items := []model.Telemetry{
		{GPUId: "gpu-1", Timestamp: base.Add(-1 * time.Hour), Metrics: map[string]float64{"temp": 70}},
		{GPUId: "gpu-1", Timestamp: base.Add(0), Metrics: map[string]float64{"temp": 71}},
		{GPUId: "gpu-1", Timestamp: base.Add(1 * time.Hour), Metrics: map[string]float64{"temp": 72}},
	}
	fs := &fakeStore{tel: map[string][]model.Telemetry{"gpu-1": items}}
	srv := newServer(fs)
	start := base.Add(-30 * time.Minute).Format(time.RFC3339)
	end := base.Add(30 * time.Minute).Format(time.RFC3339)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus/gpu-1/telemetry?start_time="+start+"&end_time="+end, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 item in window, got %d", len(got))
	}
}

func TestQueryTelemetry_BadTime(t *testing.T) {
	fs := &fakeStore{}
	srv := newServer(fs)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus/gpu-1/telemetry?start_time=not-a-time", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestQueryTelemetry_NotFoundPath(t *testing.T) {
	fs := &fakeStore{}
	srv := newServer(fs)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus/gpu-1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
