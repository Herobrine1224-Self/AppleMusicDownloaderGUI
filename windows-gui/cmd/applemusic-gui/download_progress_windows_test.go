package main

import (
	"testing"
	"time"
)

func TestDownloadProgressStatsFormatsSize(t *testing.T) {
	tests := []struct {
		name  string
		total int64
		want  string
	}{
		{name: "unknown zero", total: 0, want: "--"},
		{name: "unknown negative", total: -1, want: "--"},
		{name: "bytes", total: 512, want: "512 B"},
		{name: "kibibytes", total: 1536, want: "1.5 KiB"},
		{name: "mebibytes", total: 5 * 1024 * 1024, want: "5.0 MiB"},
		{name: "gibibytes", total: 2 * 1024 * 1024 * 1024, want: "2.0 GiB"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stats downloadProgressStats
			stats.Observe("downloading", 0, test.total, time.Unix(1, 0))
			if got := stats.SizeText(); got != test.want {
				t.Fatalf("SizeText() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDownloadProgressStatsCalculatesDownloadSpeed(t *testing.T) {
	start := time.Unix(100, 0)
	var stats downloadProgressStats

	stats.Observe("downloading", 1024, 10*1024, start)
	if got := stats.SpeedText("downloading"); got != "--" {
		t.Fatalf("first sample SpeedText() = %q, want unknown", got)
	}

	stats.Observe("downloading", 5*1024, 10*1024, start.Add(2*time.Second))
	if got := stats.SpeedText("downloading"); got != "2.0 KiB/s" {
		t.Fatalf("SpeedText() = %q, want %q", got, "2.0 KiB/s")
	}
	if got := stats.SpeedText("decrypting"); got != "--" {
		t.Fatalf("inactive phase SpeedText() = %q, want unknown", got)
	}
}

func TestDownloadProgressStatsTracksActiveDownload(t *testing.T) {
	start := time.Unix(100, 0)
	var stats downloadProgressStats
	if stats.DownloadInProgress() {
		t.Fatal("empty stats reported an active download")
	}

	stats.Observe("downloading", 512, 1024, start)
	if !stats.DownloadInProgress() {
		t.Fatal("partial known-length download was not active")
	}
	stats.Observe("downloading", 1024, 1024, start.Add(time.Second))
	if stats.DownloadInProgress() {
		t.Fatal("completed known-length download remained active")
	}

	stats.Reset()
	stats.Observe("downloading", 1024, -1, start)
	if !stats.DownloadInProgress() {
		t.Fatal("unknown-length download was not active")
	}
	stats.CompleteDownload(2048, start.Add(time.Second))
	if stats.DownloadInProgress() {
		t.Fatal("completed unknown-length download remained active")
	}
	if got := stats.SizeText(); got != "2.0 KiB" {
		t.Fatalf("completed unknown-length size = %q", got)
	}
	if got := stats.SpeedText("downloading"); got != "1.0 KiB/s" {
		t.Fatalf("completed unknown-length speed = %q", got)
	}
	stats.Observe("decrypting", 2048, -1, start.Add(2*time.Second))
	if got := stats.SizeText(); got != "2.0 KiB" {
		t.Fatalf("decrypting cleared completed unknown-length size: %q", got)
	}
}

func TestDownloadProgressStatsResetsSpeedBaseline(t *testing.T) {
	start := time.Unix(100, 0)
	tests := []struct {
		name         string
		phase        string
		current      int64
		total        int64
		observedTime time.Time
	}{
		{name: "phase change", phase: "decrypting", current: 6 * 1024, total: 10 * 1024, observedTime: start.Add(2 * time.Second)},
		{name: "current rollback", phase: "downloading", current: 512, total: 10 * 1024, observedTime: start.Add(2 * time.Second)},
		{name: "total change", phase: "downloading", current: 6 * 1024, total: 20 * 1024, observedTime: start.Add(2 * time.Second)},
		{name: "time rollback", phase: "downloading", current: 6 * 1024, total: 10 * 1024, observedTime: start.Add(-time.Second)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stats downloadProgressStats
			stats.Observe("downloading", 1024, 10*1024, start)
			stats.Observe(test.phase, test.current, test.total, test.observedTime)
			if got := stats.SpeedText("downloading"); got != "--" {
				t.Fatalf("SpeedText() = %q after reset condition, want unknown", got)
			}
		})
	}
}

func TestDownloadProgressStatsResetClearsValues(t *testing.T) {
	start := time.Unix(100, 0)
	var stats downloadProgressStats
	stats.Observe("downloading", 0, 1024, start)
	stats.Observe("downloading", 1024, 1024, start.Add(time.Second))

	stats.Reset()

	if got := stats.SizeText(); got != "--" {
		t.Fatalf("SizeText() after Reset = %q, want unknown", got)
	}
	if got := stats.SpeedText("downloading"); got != "--" {
		t.Fatalf("SpeedText() after Reset = %q, want unknown", got)
	}
}
