package routermetrics

import "time"

type Model struct {
	ID           string   `json:"id"`
	Samples      int64    `json:"sample_count"`
	Successes    int64    `json:"success_requests"`
	Failures     int64    `json:"failure_requests"`
	SuccessRate  *float64 `json:"success_rate"`
	AverageTTFT  *float64 `json:"avg_ttft_ms"`
	latencySum   float64
	latencyCount int64
}

type Snapshot struct {
	Scope            string    `json:"scope"`
	GroupID          int64     `json:"group_id"`
	Window           string    `json:"window"`
	GeneratedAt      time.Time `json:"generated_at"`
	DataThrough      time.Time `json:"data_through"`
	CoverageComplete bool      `json:"coverage_complete"`
	MinimumSamples   int64     `json:"minimum_samples"`
	Models           []Model   `json:"models"`
}
