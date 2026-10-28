package health

import (
	"context"
	"time"
)

type Component struct {
	Status          string `json:"status"`
	LatencyMS       *int64 `json:"latency_ms,omitempty"`
	Version         *uint  `json:"version,omitempty"`
	ExpectedVersion *uint  `json:"expected_version,omitempty"`
	Dirty           *bool  `json:"dirty,omitempty"`
	Error           string `json:"error,omitempty"`
	Cause           error  `json:"-"`
}

type Report struct {
	Status        string               `json:"status"`
	CheckedAt     time.Time            `json:"checked_at"`
	UptimeSeconds float64              `json:"uptime_seconds"`
	Components    map[string]Component `json:"components"`
}

type Checker interface {
	Check(context.Context) map[string]Component
}

func NewReport(components map[string]Component, startedAt, checkedAt time.Time) Report {
	status := "healthy"
	for _, component := range components {
		if component.Status != "healthy" {
			status = "unhealthy"
			break
		}
	}
	return Report{
		Status:        status,
		CheckedAt:     checkedAt.UTC(),
		UptimeSeconds: checkedAt.Sub(startedAt).Seconds(),
		Components:    components,
	}
}
