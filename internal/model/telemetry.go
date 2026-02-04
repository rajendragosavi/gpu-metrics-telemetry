package model

import "time"

type Telemetry struct {
	GPUId     string             `json:"gpu_id"`
	Timestamp time.Time          `json:"timestamp"`
	Metrics   map[string]float64 `json:"metrics"`
}
