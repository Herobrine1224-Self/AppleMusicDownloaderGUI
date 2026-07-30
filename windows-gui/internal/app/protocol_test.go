package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeBootstrapResponseUsesLastJSONLine(t *testing.T) {
	output := []byte("noise\n{\"status\":{\"installed\":true,\"distro_name\":\"AppleMusic-Runtime-12345678\"}}\n")
	response, err := DecodeBootstrapResponse(output)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Status.Installed || response.Status.DistroName != "AppleMusic-Runtime-12345678" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestDecodeDownloadEventRejectsOrdinaryLog(t *testing.T) {
	if _, ok := DecodeDownloadEvent("Downloading..."); ok {
		t.Fatal("ordinary log line was parsed as an event")
	}
	event, ok := DecodeDownloadEvent(`{"event":"progress","phase":"downloading","current":5242880,"total":10485760}`)
	if !ok || event.Event != "progress" || event.Phase != "downloading" || event.Current != 5242880 || event.Total != 10485760 {
		t.Fatalf("unexpected event: %+v, %t", event, ok)
	}
}

func TestCleanupTaskPartialsOnlyRemovesMatchingTask(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, "track.partial-0123456789abcdef.m4a")
	other := filepath.Join(root, "track.partial-fedcba9876543210.m4a")
	final := filepath.Join(root, "track.m4a")
	for _, path := range []string{owned, other, final} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cleanupTaskPartials(root, "0123456789abcdef")
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("owned partial still exists: %v", err)
	}
	for _, path := range []string{other, final} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unrelated file was changed: %s: %v", path, err)
		}
	}
}
