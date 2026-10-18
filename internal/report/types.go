package report

import (
	"fmt"
	"math"
)

// Measurement is real image sizes from `docker build`, in MB (2^20 bytes).
// BeforeMB is zero when the repository had no Dockerfile to build.
type Measurement struct {
	BeforeMB float64 `json:"beforeMB"`
	AfterMB  float64 `json:"afterMB"`

	// BeforeImage and AfterImage are the local tags that were built.
	BeforeImage string `json:"beforeImage,omitempty"`
	AfterImage  string `json:"afterImage,omitempty"`
}

// SavedPercent is the measured reduction, or 0 when there is none.
func (m *Measurement) SavedPercent() float64 {
	if m == nil || m.BeforeMB <= 0 || m.AfterMB >= m.BeforeMB {
		return 0
	}
	return math.Floor((m.BeforeMB-m.AfterMB)/m.BeforeMB*1000) / 10
}

// VulnCounts is the number of known vulnerabilities by severity.
type VulnCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

// Total is every finding.
func (v VulnCounts) Total() int { return v.Critical + v.High + v.Medium + v.Low + v.Unknown }

// Short renders "critical / high".
func (v VulnCounts) Short() string { return fmt.Sprintf("%d / %d", v.Critical, v.High) }

// VulnComparison is the scan of both images.
type VulnComparison struct {
	Scanner string     `json:"scanner"`
	Before  VulnCounts `json:"before"`
	After   VulnCounts `json:"after"`

	// HasBefore is false when there was no original image to scan.
	HasBefore bool `json:"hasBefore"`
}
