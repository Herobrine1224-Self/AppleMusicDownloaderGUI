package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	want := Settings{
		OutputDir:      filepath.Join("D:", "Music"),
		Quality:        QualityAtmos,
		SongFileFormat: "{TrackNumber}. {SongName}",
	}
	if err := store.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStoreHistoryRoundTripAndClear(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	want := []HistoryEntry{{
		CompletedAt: time.Date(2026, time.July, 30, 12, 30, 0, 0, time.UTC),
		URL:         "https://music.apple.com/cn/album/example/123",
		Path:        filepath.Join("D:", "Music", "track.m4a"),
		Artist:      "Artist",
		Album:       "Album",
		Song:        "Song",
	}}
	if err := store.SaveHistory(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadHistory()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	if err := store.SaveHistory([]HistoryEntry{}); err != nil {
		t.Fatal(err)
	}
	got, err = store.LoadHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("history length = %d, want 0", len(got))
	}
	data, err := os.ReadFile(filepath.Join(store.Dir, "download-history.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]\n" {
		t.Fatalf("cleared history JSON = %q, want %q", data, "[]\\n")
	}
}
