package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	QualityLossless = "lossless"
	QualityAtmos    = "atmos"

	SongFileFormatTitle = "{SongName}"
)

type Settings struct {
	OutputDir      string `json:"output_dir"`
	Quality        string `json:"quality"`
	SongFileFormat string `json:"song_file_format"`
}

type HistoryEntry struct {
	CompletedAt time.Time `json:"completed_at"`
	URL         string    `json:"url"`
	Path        string    `json:"path"`
	Artist      string    `json:"artist"`
	Album       string    `json:"album"`
	Song        string    `json:"song"`
}

type Store struct {
	Dir string
}

func DefaultStore() (Store, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return Store{}, errors.New("LOCALAPPDATA is not defined")
	}
	return Store{Dir: filepath.Join(local, "AppleMusicDownloader")}, nil
}

func DefaultSettings() Settings {
	home, _ := os.UserHomeDir()
	return Settings{
		OutputDir:      filepath.Join(home, "Music", "Apple Music Downloads"),
		Quality:        QualityLossless,
		SongFileFormat: SongFileFormatTitle,
	}
}

func (s Store) LoadSettings() (Settings, error) {
	settings := DefaultSettings()
	data, err := os.ReadFile(filepath.Join(s.Dir, "gui-settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return DefaultSettings(), err
	}
	if settings.OutputDir == "" {
		settings.OutputDir = DefaultSettings().OutputDir
	}
	if settings.Quality != QualityLossless && settings.Quality != QualityAtmos {
		settings.Quality = QualityLossless
	}
	if settings.SongFileFormat == "" {
		settings.SongFileFormat = SongFileFormatTitle
	}
	return settings, nil
}

func (s Store) SaveSettings(settings Settings) error {
	return s.writeJSON("gui-settings.json", settings)
}

func (s Store) LoadHistory() ([]HistoryEntry, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, "download-history.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var history []HistoryEntry
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, err
	}
	return history, nil
}

func (s Store) SaveHistory(history []HistoryEntry) error {
	if len(history) > 100 {
		history = history[:100]
	}
	return s.writeJSON("download-history.json", history)
}

func (s Store) writeJSON(name string, value any) error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(s.Dir, name)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
