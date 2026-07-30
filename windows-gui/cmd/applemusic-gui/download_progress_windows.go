package main

import (
	"fmt"
	"math"
	"time"
)

const (
	downloadProgressPhase = "downloading"
	downloadCompletePhase = "download_complete"
)

type downloadProgressStats struct {
	total           int64
	downloadCurrent int64
	downloadTotal   int64
	hasDownload     bool

	sampleCurrent int64
	sampleTotal   int64
	sampleTime    time.Time
	hasSample     bool

	bytesPerSecond float64
	hasSpeed       bool
}

func (s *downloadProgressStats) Reset() {
	*s = downloadProgressStats{}
}

func (s *downloadProgressStats) Observe(phase string, current, total int64, now time.Time) {
	if phase == downloadProgressPhase || total > 0 {
		s.total = total
	}
	if phase != downloadProgressPhase || current < 0 || now.IsZero() {
		s.hasDownload = false
		s.resetSpeedSample()
		return
	}
	s.downloadCurrent = current
	s.downloadTotal = total
	s.hasDownload = true

	if !s.hasSample || total != s.sampleTotal || current < s.sampleCurrent || !now.After(s.sampleTime) {
		s.startSpeedSample(current, total, now)
		return
	}

	elapsed := now.Sub(s.sampleTime).Seconds()
	s.bytesPerSecond = float64(current-s.sampleCurrent) / elapsed
	s.hasSpeed = true
	s.sampleCurrent = current
	s.sampleTime = now
}

func (s *downloadProgressStats) SizeText() string {
	if s.total <= 0 {
		return "--"
	}
	return formatByteValue(float64(s.total), "")
}

func (s *downloadProgressStats) SpeedText(activePhase string) string {
	if activePhase != downloadProgressPhase || !s.hasSpeed {
		return "--"
	}
	return formatByteValue(s.bytesPerSecond, "/s")
}

func (s *downloadProgressStats) DownloadInProgress() bool {
	if !s.hasDownload {
		return false
	}
	return s.downloadTotal <= 0 || s.downloadCurrent < s.downloadTotal
}

func (s *downloadProgressStats) CompleteDownload(current int64, now time.Time) {
	if current >= 0 && s.total <= 0 {
		s.total = current
	}
	if s.hasSample && current > s.sampleCurrent && now.After(s.sampleTime) {
		elapsed := now.Sub(s.sampleTime).Seconds()
		s.bytesPerSecond = float64(current-s.sampleCurrent) / elapsed
		s.hasSpeed = true
		s.sampleCurrent = current
		s.sampleTime = now
	}
	s.downloadCurrent = current
	s.downloadTotal = current
	s.hasDownload = false
}

func (s *downloadProgressStats) startSpeedSample(current, total int64, now time.Time) {
	s.sampleCurrent = current
	s.sampleTotal = total
	s.sampleTime = now
	s.hasSample = true
	s.bytesPerSecond = 0
	s.hasSpeed = false
}

func (s *downloadProgressStats) resetSpeedSample() {
	s.sampleCurrent = 0
	s.sampleTotal = 0
	s.sampleTime = time.Time{}
	s.hasSample = false
	s.bytesPerSecond = 0
	s.hasSpeed = false
}

func formatByteValue(value float64, suffix string) string {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "--"
	}
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f %s%s", value, units[unit], suffix)
	}
	return fmt.Sprintf("%.1f %s%s", value, units[unit], suffix)
}
