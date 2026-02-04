package storage

import (
	"sort"
	"sync"
	"time"

	"gpu-metric-collector/internal/model"
)

// MemoryStore is a threadsafe in-memory implementation of Store.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]model.Telemetry // gpuID -> ordered by time asc
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]model.Telemetry)}
}

func (m *MemoryStore) SaveTelemetry(t model.Telemetry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.data[t.GPUId]
	s = append(s, t)
	// maintain order by timestamp (append then sort stable; small overhead acceptable for demo)
	sort.SliceStable(s, func(i, j int) bool { return s[i].Timestamp.Before(s[j].Timestamp) })
	m.data[t.GPUId] = s
	return nil
}

func (m *MemoryStore) ListGPUs() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.data))
	for id := range m.data {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemoryStore) QueryTelemetry(gpuID string, start, end *time.Time) ([]model.Telemetry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.data[gpuID]
	if start == nil && end == nil {
		out := make([]model.Telemetry, len(s))
		copy(out, s)
		return out, nil
	}
	var out []model.Telemetry
	for _, it := range s {
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
