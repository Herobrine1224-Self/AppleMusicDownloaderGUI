//go:build windows

package main

import (
	"applemusic/gui/internal/app"
	"errors"
	"os"
	"strings"

	"github.com/lxn/walk"
)

const missingHistoryFileMessage = "文件已被移动或删除，无法打开路径。"

type historyRow struct {
	Title     string
	Artist    string
	Album     string
	Completed string
	Path      string
}

type historyModel struct {
	walk.ReflectTableModelBase
	rows []historyRow
}

func (m *historyModel) Items() any {
	return m.rows
}

func (m *historyModel) Set(entries []app.HistoryEntry) {
	m.rows = make([]historyRow, 0, len(entries))
	for _, entry := range entries {
		title := entry.Song
		if title == "" {
			title = entry.Album
		}
		m.rows = append(m.rows, historyRow{
			Title:     title,
			Artist:    entry.Artist,
			Album:     entry.Album,
			Completed: entry.CompletedAt.Local().Format("2006-01-02 15:04"),
			Path:      entry.Path,
		})
	}
	m.PublishRowsReset()
}

func (m *historyModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.rows) {
		return ""
	}
	item := m.rows[row]
	switch col {
	case 0:
		return item.Title
	case 1:
		return item.Artist
	case 2:
		return item.Album
	case 3:
		return item.Completed
	case 4:
		return item.Path
	}
	return ""
}

func historyWithoutIndex(entries []app.HistoryEntry, index int) ([]app.HistoryEntry, bool) {
	if index < 0 || index >= len(entries) {
		return entries, false
	}
	next := make([]app.HistoryEntry, 0, len(entries)-1)
	next = append(next, entries[:index]...)
	next = append(next, entries[index+1:]...)
	return next, true
}

func existingHistoryPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", os.ErrNotExist
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

func historyPathErrorMessage(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return missingHistoryFileMessage
	}
	return "无法访问记录中的文件。\r\n\r\n" + err.Error()
}
