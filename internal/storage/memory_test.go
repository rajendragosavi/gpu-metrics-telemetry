package storage

import (
	"testing"
	"time"

	"gpu-metric-collector/internal/model"
)

func TestMemoryStore_SaveAndQueryOrder(t *testing.T) {
	st := NewMemoryStore()
	t0 := time.Now()
	in := []model.Telemetry{
		{GPUId: "g1", Timestamp: t0.Add(2 * time.Second), Metrics: map[string]float64{"a": 2}},
		{GPUId: "g1", Timestamp: t0.Add(1 * time.Second), Metrics: map[string]float64{"a": 1}},
		{GPUId: "g1", Timestamp: t0.Add(3 * time.Second), Metrics: map[string]float64{"a": 3}},
	}
	for _, x := range in {
		if err := st.SaveTelemetry(x); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	out, err := st.QueryTelemetry("g1", nil, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 got %d", len(out))
	}
	if !out[0].Timestamp.Before(out[1].Timestamp) || !out[1].Timestamp.Before(out[2].Timestamp) {
		t.Fatalf("not ordered ascending by time: %#v", out)
	}
}

func TestMemoryStore_ListGPUs(t *testing.T) {
	st := NewMemoryStore()
	_ = st.SaveTelemetry(model.Telemetry{GPUId: "b", Timestamp: time.Now()})
	_ = st.SaveTelemetry(model.Telemetry{GPUId: "a", Timestamp: time.Now()})
	ids, err := st.ListGPUs()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("unexpected ids: %#v", ids)
	}
}

func TestMemoryStore_QueryWindow(t *testing.T) {
	st := NewMemoryStore()
	t0 := time.Now()
	for i := 0; i < 5; i++ {
		_ = st.SaveTelemetry(model.Telemetry{GPUId: "g1", Timestamp: t0.Add(time.Duration(i) * time.Second)})
	}
	start := t0.Add(1 * time.Second)
	end := t0.Add(3 * time.Second)
	out, err := st.QueryTelemetry("g1", &start, &end)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 got %d", len(out))
	}
}
