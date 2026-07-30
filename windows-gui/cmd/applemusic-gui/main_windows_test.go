//go:build windows

package main

import (
	"applemusic/gui/internal/app"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOutputDirDialogDoesNotRestrictNavigation(t *testing.T) {
	dialog := newOutputDirDialog()
	if dialog.InitialDirPath != "" {
		t.Fatalf("InitialDirPath = %q, want empty so the folder browser is not rooted at the current output directory", dialog.InitialDirPath)
	}
}

func TestDownloadFailureMessageDistinguishesTimeout(t *testing.T) {
	if got := downloadFailureMessage(context.DeadlineExceeded); got != "下载超时，请重试" {
		t.Fatalf("timeout message = %q", got)
	}
	if got := downloadFailureMessage(errors.New("network failed")); got != "network failed" {
		t.Fatalf("generic message = %q", got)
	}
}

func TestHistoryWithoutIndex(t *testing.T) {
	original := []app.HistoryEntry{{Song: "one"}, {Song: "two"}, {Song: "three"}}
	tests := []struct {
		name    string
		index   int
		want    []string
		removed bool
	}{
		{name: "first", index: 0, want: []string{"two", "three"}, removed: true},
		{name: "middle", index: 1, want: []string{"one", "three"}, removed: true},
		{name: "last", index: 2, want: []string{"one", "two"}, removed: true},
		{name: "negative", index: -1, want: []string{"one", "two", "three"}},
		{name: "past end", index: 3, want: []string{"one", "two", "three"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, removed := historyWithoutIndex(original, test.index)
			if removed != test.removed {
				t.Fatalf("removed = %v, want %v", removed, test.removed)
			}
			if songs := historySongs(got); !reflect.DeepEqual(songs, test.want) {
				t.Fatalf("songs = %v, want %v", songs, test.want)
			}
			if songs := historySongs(original); !reflect.DeepEqual(songs, []string{"one", "two", "three"}) {
				t.Fatalf("original history was modified: %v", songs)
			}
		})
	}
}

func TestExistingHistoryPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track.m4a")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := existingHistoryPath("  " + path + "  ")
	if err != nil {
		t.Fatalf("existingHistoryPath returned an error: %v", err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}

	if _, err := existingHistoryPath(filepath.Join(t.TempDir(), "missing.m4a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing path error = %v, want os.ErrNotExist", err)
	}
	if _, err := existingHistoryPath("  "); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty path error = %v, want os.ErrNotExist", err)
	}
	if got := historyPathErrorMessage(os.ErrNotExist); got != "文件已被移动或删除，无法打开路径。" {
		t.Fatalf("missing path message = %q", got)
	}
}

func historySongs(entries []app.HistoryEntry) []string {
	songs := make([]string, len(entries))
	for i, entry := range entries {
		songs[i] = entry.Song
	}
	return songs
}
