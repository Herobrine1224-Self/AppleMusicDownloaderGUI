package bootstrap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type StateStore struct {
	Path string
}

func (s StateStore) Load() (State, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, os.ErrNotExist
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	if state.SchemaVersion != 1 || state.ProductID != ProductID || state.OwnerSID == "" || state.InstanceID == "" || state.DistroName == "" {
		return State{}, errors.New("state file does not describe an AppleMusic managed runtime")
	}
	instanceBytes, decodeErr := hex.DecodeString(state.InstanceID)
	if decodeErr != nil || len(instanceBytes) != 16 || !strings.EqualFold(state.DistroName, DistroPrefix+state.InstanceID[:8]) {
		return State{}, errors.New("state file contains an invalid managed runtime identity")
	}
	return state, nil
}

func (s StateStore) Save(state State) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".bootstrap-state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, s.Path)
}

func newState(config Config, now time.Time) (State, error) {
	ownerSID, err := CurrentUserSID()
	if err != nil {
		return State{}, err
	}
	if ownerSID == "S-1-5-18" || ownerSID == "S-1-5-19" || ownerSID == "S-1-5-20" {
		return State{}, errors.New("the managed WSL runtime must be installed by an interactive user, not a Windows service account")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return State{}, err
	}
	instanceID := hex.EncodeToString(random)
	distroName := DistroPrefix + instanceID[:8]
	return State{
		SchemaVersion:    1,
		ProductID:        ProductID,
		OwnerSID:         ownerSID,
		InstanceID:       instanceID,
		DistroName:       distroName,
		InstallDir:       filepath.Join(config.AppDataDir, "wsl", distroName),
		Stage:            StagePrepared,
		RuntimeVersion:   config.RuntimeVersion,
		PayloadSHA256:    config.PayloadHash,
		UbuntuBaseSHA256: config.UbuntuBaseHash,
		CreatedAt:        now.UTC(),
	}, nil
}
