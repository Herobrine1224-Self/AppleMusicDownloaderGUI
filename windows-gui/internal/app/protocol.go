package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type BootstrapStatus struct {
	Installed      bool     `json:"installed"`
	Owned          bool     `json:"owned"`
	Running        bool     `json:"running"`
	Healthy        bool     `json:"healthy"`
	Stage          string   `json:"stage"`
	InstanceID     string   `json:"instance_id"`
	DistroName     string   `json:"distro_name"`
	InstallDir     string   `json:"install_dir"`
	RuntimeVersion string   `json:"runtime_version"`
	LogPath        string   `json:"log_path"`
	Detail         string   `json:"detail"`
	RecoveryPaths  []string `json:"recovery_paths"`
}

type BootstrapError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BootstrapResponse struct {
	Status     BootstrapStatus `json:"status"`
	Error      *BootstrapError `json:"error,omitempty"`
	Stopped    bool            `json:"stopped,omitempty"`
	Removed    bool            `json:"removed,omitempty"`
	BackupPath string          `json:"backup_path,omitempty"`
}

func DecodeBootstrapResponse(output []byte) (BootstrapResponse, error) {
	lines := bytes.Split(bytes.TrimSpace(output), []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(bytes.TrimPrefix(lines[i], []byte{0xef, 0xbb, 0xbf}))
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var response BootstrapResponse
		if err := json.Unmarshal(line, &response); err == nil {
			return response, nil
		}
	}
	return BootstrapResponse{}, errors.New("bootstrap did not return a JSON response")
}

type DownloadedTrack struct {
	Path     string `json:"path"`
	Artist   string `json:"artist"`
	ArtistID string `json:"artist_id"`
	Album    string `json:"album"`
	Song     string `json:"song"`
}

type DownloadEvent struct {
	Event    string            `json:"event"`
	Phase    string            `json:"phase,omitempty"`
	Message  string            `json:"message,omitempty"`
	Detail   string            `json:"detail,omitempty"`
	URL      string            `json:"url,omitempty"`
	Path     string            `json:"path,omitempty"`
	Artist   string            `json:"artist,omitempty"`
	Album    string            `json:"album,omitempty"`
	Song     string            `json:"song,omitempty"`
	Current  int64             `json:"current,omitempty"`
	Total    int64             `json:"total,omitempty"`
	Success  int               `json:"success,omitempty"`
	Warnings int               `json:"warnings,omitempty"`
	Errors   int               `json:"errors,omitempty"`
	Skipped  bool              `json:"skipped,omitempty"`
	Tracks   []DownloadedTrack `json:"tracks,omitempty"`
}

func DecodeDownloadEvent(line string) (DownloadEvent, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
	if line == "" || !strings.HasPrefix(line, "{") {
		return DownloadEvent{}, false
	}
	var event DownloadEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil || event.Event == "" {
		return DownloadEvent{}, false
	}
	return event, true
}

type OperationError struct {
	Code     string
	Message  string
	ExitCode int
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("process exited with code %d", e.ExitCode)
}
