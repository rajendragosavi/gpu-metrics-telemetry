package storage

import (
	"time"

	"gpu-metric-collector/internal/model"
)

type Store interface {
	SaveTelemetry(t model.Telemetry) error
	ListGPUs() ([]string, error)
	QueryTelemetry(gpuID string, start, end *time.Time) ([]model.Telemetry, error)
}
