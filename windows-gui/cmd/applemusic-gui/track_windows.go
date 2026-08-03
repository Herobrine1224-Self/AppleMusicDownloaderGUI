//go:build windows

package main

import (
	"fmt"
	"sync"

	"applemusic/gui/internal/app"

	"github.com/lxn/walk"
)

type trackRow struct {
	No     string
	Title  string
	Artist string
	Album  string
}

type trackTableModel struct {
	walk.ReflectTableModelBase
	rows    []trackRow
	checker *trackChecker
}

func newTrackTableModel() *trackTableModel {
	return &trackTableModel{}
}

func (m *trackTableModel) Items() any {
	return m.rows
}

func (m *trackTableModel) RowCount() int {
	return len(m.rows)
}

func (m *trackTableModel) Set(groups []app.TrackGroup) {
	rows := make([]trackRow, 0, len(groups))
	for _, group := range groups {
		for _, track := range group.Tracks {
			title := track.Name
			artist := track.Artist
			if artist == "" {
				artist = group.Artist
			}
			album := track.Album
			if album == "" {
				album = group.Title
			}
			rows = append(rows, trackRow{
				No:     fmt.Sprintf("%d", track.Index),
				Title:  title,
				Artist: artist,
				Album:  album,
			})
		}
	}
	m.rows = rows
	m.PublishRowsReset()
}

func (m *trackTableModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.rows) {
		return ""
	}
	item := m.rows[row]
	switch col {
	case 0:
		if m.checker != nil && m.checker.Checked(row) {
			return "✓"
		}
		return ""
	case 1:
		return item.No
	case 2:
		return item.Title
	case 3:
		return item.Artist
	case 4:
		return item.Album
	}
	return ""
}

func (m *trackTableModel) StyleCell(style *walk.CellStyle) {
	if m.checker != nil && m.checker.Checked(style.Row()) {
		style.BackgroundColor = walk.RGB(255, 228, 232)
	}
}

func (m *trackTableModel) Clear() {
	if len(m.rows) == 0 {
		return
	}
	m.rows = nil
	m.PublishRowsReset()
}

// trackChecker maps 0-based table rows to 1-based global track indexes.
type trackChecker struct {
	mu      sync.Mutex
	total   int
	checked map[int]bool
}

func newTrackChecker() *trackChecker {
	return &trackChecker{checked: make(map[int]bool)}
}

func (c *trackChecker) Reset(total int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total = total
	c.checked = make(map[int]bool)
}

func (c *trackChecker) Checked(row int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checked[row+1]
}

func (c *trackChecker) SetChecked(row int, checked bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := row + 1
	if checked {
		c.checked[index] = true
	} else {
		delete(c.checked, index)
	}
	return nil
}

func (c *trackChecker) SetAll(checked bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = make(map[int]bool)
	if checked {
		for i := 1; i <= c.total; i++ {
			c.checked[i] = true
		}
	}
}

func (c *trackChecker) SelectedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.checked)
}

func (c *trackChecker) SelectedIndexes() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	indexes := make([]int, 0, len(c.checked))
	for i := 1; i <= c.total; i++ {
		if c.checked[i] {
			indexes = append(indexes, i)
		}
	}
	return indexes
}
