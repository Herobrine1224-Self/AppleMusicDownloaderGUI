package bootstrap

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	archivepath "path"
	"path/filepath"
	"strings"
	"time"
)

type Manager struct {
	Config    Config
	Runner    Runner
	WSL       WSLClient
	Artifacts ArtifactManager
	Store     StateStore
	Locker    Locker
	Now       func() time.Time
}

const incompleteImportGracePeriod = 2 * time.Minute

func NewManager(config Config) (*Manager, error) {
	wslPath, err := SystemExecutable("wsl.exe")
	if err != nil {
		// The optional component may not be enabled yet, but modern Windows still
		// normally ships the command stub. Keep the canonical path for elevation.
		root := os.Getenv("SystemRoot")
		if root == "" {
			return nil, err
		}
		wslPath = filepath.Join(root, "System32", "wsl.exe")
	}
	runner := OSRunner{Timeout: config.CommandTimeout}
	wsl := WSLClient{Runner: runner, WSLPath: wslPath}
	ownerSID, err := CurrentUserSID()
	if err != nil {
		return nil, err
	}
	return &Manager{
		Config:    config,
		Runner:    runner,
		WSL:       wsl,
		Artifacts: ArtifactManager{Config: config},
		Store:     StateStore{Path: filepath.Join(config.AppDataDir, "bootstrap-state.json")},
		Locker:    NamedMutex{Name: managedMutexName(ownerSID)},
		Now:       time.Now,
	}, nil
}

func managedMutexName(ownerSID string) string {
	digest := sha256.Sum256([]byte(strings.ToUpper(ownerSID)))
	return `Global\AppleMusicDownloader.WSLBootstrap.` + hex.EncodeToString(digest[:8])
}

func (m *Manager) Install(ctx context.Context) (Status, error) {
	if err := ValidateNativePlatform(); err != nil {
		return Status{}, err
	}
	if err := m.Artifacts.VerifyPayload(); err != nil {
		return Status{}, err
	}
	unlock, err := m.Locker.Lock(ctx)
	if err != nil {
		return Status{}, Wrap(CodeConcurrentInstall, "lock installer", err)
	}
	defer unlock()

	state, err := m.loadOrCreateState()
	if err != nil {
		return Status{}, err
	}
	if err := ensureLocalInstallPath(m.Config.AppDataDir, state.InstallDir); err != nil {
		return Status{}, Wrap(CodeRepairRequired, "validate install directory", err)
	}

	platformReady, err := m.WSL.PlatformReady(ctx)
	if err != nil {
		return Status{}, Wrap(CodePlatform, "probe WSL platform", err)
	}
	if !platformReady {
		state.Stage = StagePlatformPending
		_ = m.Store.Save(state)
		return statusFromState(state), Wrap(CodePlatform, "probe WSL platform", errors.New("WSL optional components are not ready; run the elevated platform helper"))
	}

	reconcileAttempts := 0

checkManagedDistribution:
	distros, err := m.WSL.List(ctx, false)
	if err != nil {
		return Status{}, Wrap(CodeCommand, "list WSL distributions", err)
	}
	if containsDistro(distros, state.DistroName) {
		wasRunning := false
		if state.Stage == StageInstalled {
			running, runningErr := m.WSL.List(ctx, true)
			if runningErr != nil {
				return statusFromState(state), Wrap(CodeCommand, "read managed runtime state", runningErr)
			}
			wasRunning = containsDistro(running, state.DistroName)
		}
		if err := m.WSL.VerifyOwner(ctx, state); err != nil {
			return statusFromState(state), Wrap(CodeNameConflict, "verify existing distribution", err)
		}
		if state.Stage == StageInstalled {
			if !wasRunning {
				if err := m.WSL.Terminate(ctx, state.DistroName); err != nil {
					return statusFromState(state), Wrap(CodeCommand, "restore stopped runtime state", err)
				}
			}
			status := statusFromState(state)
			status.Installed = true
			status.Owned = true
			status.Running = wasRunning
			status.Healthy = wasRunning && runtimeHealthy(ctx)
			return status, nil
		}
		return m.finishExistingInstall(ctx, state)
	}

	state, reconciled, err := m.reconcileUnregisteredInstall(ctx, state)
	if err != nil {
		return statusFromState(state), err
	}
	if reconciled {
		reconcileAttempts++
		if reconcileAttempts > 2 {
			return statusFromState(state), Wrap(CodeRepairRequired, "reconcile interrupted WSL import", errors.New("managed distribution state changed repeatedly; retry after WSL finishes pending work"))
		}
		goto checkManagedDistribution
	}

	baseArchive, err := m.Artifacts.ResolveUbuntuBase(ctx)
	if err != nil {
		return statusFromState(state), err
	}
	runtimeArchive, runtimeHash, err := m.Artifacts.BuildRuntimeArchive(baseArchive, state)
	if err != nil {
		return statusFromState(state), Wrap(CodeIntegrity, "build managed WSL runtime", err)
	}
	state.RuntimeTarSHA256 = runtimeHash
	state.Stage = StageRuntimeBuilt
	if err := m.Store.Save(state); err != nil {
		return Status{}, err
	}
	if err := verifyFileSHA256(runtimeArchive, runtimeHash); err != nil {
		return statusFromState(state), Wrap(CodeIntegrity, "verify managed WSL runtime before import", err)
	}
	if err := os.MkdirAll(filepath.Dir(state.InstallDir), 0700); err != nil {
		return Status{}, err
	}
	if err := m.WSL.Import(ctx, state.DistroName, state.InstallDir, runtimeArchive); err != nil {
		return statusFromState(state), Wrap(CodeCommand, "import managed WSL runtime", err)
	}
	state.Stage = StageDistroRegistered
	if err := m.Store.Save(state); err != nil {
		return Status{}, err
	}
	return m.finishExistingInstall(ctx, state)
}

func (m *Manager) finishExistingInstall(ctx context.Context, state State) (Status, error) {
	if err := m.WSL.VerifyOwner(ctx, state); err != nil {
		return statusFromState(state), err
	}
	// The runtime image deliberately names a non-login default user. Create it
	// explicitly while all application commands continue to use --user root.
	result, err := m.WSL.Exec(ctx, state.DistroName, "root", "/usr/bin/id", "-u", "applemusic-runtime")
	if err != nil || result.ExitCode != 0 {
		if _, createErr := m.WSL.Exec(ctx, state.DistroName, "root", "/usr/sbin/useradd", "--system", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", "applemusic-runtime"); createErr != nil {
			return statusFromState(state), Wrap(CodeCommand, "create restricted runtime user", createErr)
		}
	}
	if err := m.WSL.SmokeTest(ctx, state); err != nil {
		return statusFromState(state), Wrap(CodeRuntimeNotReady, "run wrapper smoke test", err)
	}
	if err := m.WSL.Terminate(ctx, state.DistroName); err != nil {
		return statusFromState(state), Wrap(CodeCommand, "apply private WSL configuration", err)
	}
	state.Stage = StageInstalled
	if state.InstalledAt.IsZero() {
		state.InstalledAt = m.Now().UTC()
	}
	if err := m.Store.Save(state); err != nil {
		return Status{}, err
	}
	status := statusFromState(state)
	status.Installed = true
	status.Owned = true
	return status, nil
}

func (m *Manager) loadOrCreateState() (State, error) {
	state, err := m.Store.Load()
	if err == nil {
		if state.Stage == StageRemoved {
			next, createErr := newState(m.Config, m.Now())
			if createErr != nil {
				return State{}, createErr
			}
			next.RecoveryPaths = append([]string(nil), state.RecoveryPaths...)
			if saveErr := m.Store.Save(next); saveErr != nil {
				return State{}, saveErr
			}
			return next, nil
		}
		if state.Stage == StageRemovalPrepared {
			return State{}, Wrap(CodeRepairRequired, "resume bootstrap state", fmt.Errorf("runtime removal already has a verified backup at %s; run remove again to finish", state.LastBackupPath))
		}
		if state.RuntimeVersion != m.Config.RuntimeVersion || state.PayloadSHA256 != m.Config.PayloadHash || state.UbuntuBaseSHA256 != m.Config.UbuntuBaseHash {
			return State{}, Wrap(CodeRepairRequired, "resume bootstrap state", errors.New("the saved runtime version or artifact hashes differ from this bootstrap build; explicit upgrade handling is required"))
		}
		if err := validateStateOwner(state); err != nil {
			return State{}, err
		}
		if err := validateManagedStateLayout(m.Config, state); err != nil {
			return State{}, Wrap(CodeRepairRequired, "validate managed runtime state", err)
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return State{}, Wrap(CodeRepairRequired, "load bootstrap state", err)
	}
	return newAndSaveState(m.Store, m.Config, m.Now())
}

func (m *Manager) reconcileUnregisteredInstall(ctx context.Context, state State) (State, bool, error) {
	installExists, err := pathExists(state.InstallDir)
	if err != nil {
		return state, false, err
	}
	recoveryPath := m.incompleteRecoveryPath(state)
	recoveryExists, err := pathExists(recoveryPath)
	if err != nil {
		return state, false, err
	}
	if !installExists && !recoveryExists {
		return state, false, nil
	}
	if state.Stage != StageRuntimeBuilt {
		return state, false, Wrap(CodeRepairRequired, "reconcile interrupted WSL import", fmt.Errorf("unregistered managed data exists at stage %s; it was left untouched", state.Stage))
	}
	if recoveryExists {
		if installExists {
			return state, false, Wrap(CodeRepairRequired, "reconcile interrupted WSL import", errors.New("both the managed install directory and its recovery directory exist; neither was modified"))
		}
		if err := validatePlainDirectory(recoveryPath); err != nil {
			return state, false, Wrap(CodeRepairRequired, "validate recovered WSL data", err)
		}
		next, err := m.rotateStateAfterRecovery(state, recoveryPath)
		return next, err == nil, err
	}
	if err := validatePlainDirectory(filepath.Dir(state.InstallDir)); err != nil {
		return state, false, Wrap(CodeRepairRequired, "validate WSL install parent", err)
	}
	newest, err := newestTreeModTime(state.InstallDir)
	if err != nil {
		return state, false, Wrap(CodeRepairRequired, "inspect interrupted WSL import", err)
	}
	age := m.Now().Sub(newest)
	if age < incompleteImportGracePeriod {
		remaining := incompleteImportGracePeriod - age
		if remaining < 0 {
			remaining = incompleteImportGracePeriod
		}
		return state, false, Wrap(CodeRepairRequired, "wait for interrupted WSL import", fmt.Errorf("the unregistered install directory is still recent; retry in about %s and it will be preserved automatically", remaining.Round(time.Second)))
	}

	// A timed-out wsl.exe can leave the service-side import running. Recheck as
	// close as possible to the rename; an open VHD also makes os.Rename fail.
	distros, err := m.WSL.List(ctx, false)
	if err != nil {
		return state, false, Wrap(CodeCommand, "recheck WSL distributions before recovery", err)
	}
	if containsDistro(distros, state.DistroName) {
		return state, true, nil
	}
	recoveryRoot := filepath.Dir(recoveryPath)
	if err := os.MkdirAll(recoveryRoot, 0700); err != nil {
		return state, false, err
	}
	if err := validatePlainDirectory(recoveryRoot); err != nil {
		return state, false, Wrap(CodeRepairRequired, "validate WSL recovery directory", err)
	}
	if err := os.Rename(state.InstallDir, recoveryPath); err != nil {
		return state, false, Wrap(CodeRepairRequired, "preserve interrupted WSL import", err)
	}
	next, err := m.rotateStateAfterRecovery(state, recoveryPath)
	if err != nil {
		return state, false, err
	}
	return next, true, nil
}

func (m *Manager) incompleteRecoveryPath(state State) string {
	return filepath.Join(m.Config.AppDataDir, "recovery", state.DistroName+"-"+state.InstanceID+".incomplete")
}

func (m *Manager) rotateStateAfterRecovery(previous State, recoveryPath string) (State, error) {
	next, err := newState(m.Config, m.Now())
	if err != nil {
		return State{}, err
	}
	next.RecoveryPaths = appendUniquePath(previous.RecoveryPaths, recoveryPath)
	if err := m.Store.Save(next); err != nil {
		return State{}, Wrap(CodeRepairRequired, "record recovered WSL data", err)
	}
	return next, nil
}

func appendUniquePath(paths []string, candidate string) []string {
	result := append([]string(nil), paths...)
	for _, existing := range result {
		if strings.EqualFold(filepath.Clean(existing), filepath.Clean(candidate)) {
			return result
		}
	}
	return append(result, candidate)
}

func validatePlainDirectory(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path is not a plain directory: %s", name)
	}
	reparse, err := isReparsePoint(name)
	if err != nil {
		return err
	}
	if reparse {
		return fmt.Errorf("path is a reparse point: %s", name)
	}
	return nil
}

func newestTreeModTime(root string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("interrupted import contains a symbolic link or junction: %s", name)
		}
		reparse, err := isReparsePoint(name)
		if err != nil {
			return err
		}
		if reparse {
			return fmt.Errorf("interrupted import contains a reparse point: %s", name)
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

func validateManagedStateLayout(config Config, state State) error {
	if !strings.EqualFold(state.DistroName, DistroPrefix+state.InstanceID[:8]) {
		return errors.New("distribution name does not match the managed instance ID")
	}
	expected, err := filepath.Abs(filepath.Join(config.AppDataDir, "wsl", state.DistroName))
	if err != nil {
		return err
	}
	actual, err := filepath.Abs(state.InstallDir)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(actual), filepath.Clean(expected)) {
		return fmt.Errorf("install directory is not the exact managed path %s", expected)
	}
	return nil
}

func newAndSaveState(store StateStore, config Config, now time.Time) (State, error) {
	state, err := newState(config, now)
	if err != nil {
		return State{}, err
	}
	if err := store.Save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	state, err := m.Store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return Status{Detail: "not installed"}, nil
	}
	if err != nil {
		return Status{}, Wrap(CodeRepairRequired, "load bootstrap state", err)
	}
	if err := validateStateOwner(state); err != nil {
		return statusFromState(state), err
	}
	if err := validateManagedStateLayout(m.Config, state); err != nil {
		return statusFromState(state), Wrap(CodeRepairRequired, "validate managed runtime state", err)
	}
	status := statusFromState(state)
	if state.Stage == StageRemoved {
		status.Detail = "managed distribution was removed after a verified backup"
		return status, nil
	}
	distros, err := m.WSL.List(ctx, false)
	if err != nil {
		return status, Wrap(CodePlatform, "list WSL distributions", err)
	}
	if !containsDistro(distros, state.DistroName) {
		status.Detail = "managed distribution is not registered"
		return status, nil
	}
	status.Installed = true
	running, err := m.WSL.List(ctx, true)
	if err != nil {
		return status, Wrap(CodeCommand, "read managed runtime state", err)
	}
	status.Running = containsDistro(running, state.DistroName)
	if !status.Running {
		status.Detail = "a distribution with the managed name is registered but stopped; ownership will be rechecked before start, stop, or removal"
		return status, nil
	}
	if err := m.WSL.VerifyOwner(ctx, state); err != nil {
		status.Detail = err.Error()
		return status, err
	}
	status.Owned = true
	status.Healthy = runtimeHealthy(ctx)
	return status, nil
}

func (m *Manager) Start(ctx context.Context) (Status, error) {
	unlock, err := m.Locker.Lock(ctx)
	if err != nil {
		return Status{}, Wrap(CodeConcurrentInstall, "lock runtime manager", err)
	}
	defer unlock()
	state, err := m.requireOwnedState(ctx)
	if err != nil {
		return Status{}, err
	}
	if runtimeHealthy(ctx) {
		status := statusFromState(state)
		status.Installed, status.Owned, status.Running, status.Healthy = true, true, true, true
		return status, nil
	}
	loginReady, err := m.WSL.HasLoginState(ctx, state)
	if err != nil {
		return statusFromState(state), Wrap(CodeCommand, "check wrapper login state", err)
	}
	if !loginReady {
		_ = m.WSL.Terminate(ctx, state.DistroName)
		return statusFromState(state), Wrap(CodeLoginRequired, "start wrapper", errors.New("this private runtime has not completed Apple Music login; run login first"))
	}
	if portInUse("127.0.0.1:10020") || portInUse("127.0.0.1:20020") || portInUse("127.0.0.1:30020") {
		// A previously requested wrapper may still be completing network login.
		// Give that single instance time to become healthy before reporting a
		// conflict. The launcher also uses an atomic /run lock as a second guard.
		deadline := time.Now().Add(m.Config.StartupTimeout)
		for time.Now().Before(deadline) {
			if runtimeHealthy(ctx) {
				status := statusFromState(state)
				status.Installed, status.Owned, status.Running, status.Healthy = true, true, true, true
				return status, nil
			}
			select {
			case <-ctx.Done():
				return Status{}, ctx.Err()
			case <-time.After(time.Second):
			}
		}
		return statusFromState(state), Wrap(CodePortConflict, "start wrapper", errors.New("one or more required localhost ports are occupied by an unhealthy process"))
	}
	logDir := filepath.Join(m.Config.AppDataDir, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return Status{}, err
	}
	logPath := filepath.Join(logDir, "wrapper.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return Status{}, err
	}
	process, err := m.WSL.StartWrapper(state, logFile, logFile)
	if err != nil {
		logFile.Close()
		return Status{}, Wrap(CodeCommand, "launch wrapper", err)
	}
	_ = process.Release()
	_ = logFile.Close()

	deadline := time.Now().Add(m.Config.StartupTimeout)
	for time.Now().Before(deadline) {
		if runtimeHealthy(ctx) {
			status := statusFromState(state)
			status.Installed, status.Owned, status.Running, status.Healthy = true, true, true, true
			status.LogPath = logPath
			return status, nil
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return statusFromState(state), Wrap(CodeRuntimeNotReady, "wait for wrapper services", fmt.Errorf("startup timed out; inspect %s", logPath))
}

func (m *Manager) Login(ctx context.Context, username, password string) (Status, error) {
	if err := validateLoginCredentials(username, password); err != nil {
		return Status{}, Wrap(CodeLoginFailed, "validate login credentials", err)
	}
	unlock, err := m.Locker.Lock(ctx)
	if err != nil {
		return Status{}, Wrap(CodeConcurrentInstall, "lock runtime manager", err)
	}
	defer unlock()
	state, err := m.requireOwnedState(ctx)
	if err != nil {
		return Status{}, err
	}
	if runtimeHealthy(ctx) {
		return healthyStatus(state, ""), nil
	}
	// A fresh login replaces any failed or pending wrapper only inside this
	// managed distribution. It does not affect other WSL distributions.
	if err := m.WSL.Terminate(ctx, state.DistroName); err != nil {
		return statusFromState(state), Wrap(CodeCommand, "reset managed runtime before login", err)
	}
	if portInUse("127.0.0.1:10020") || portInUse("127.0.0.1:20020") || portInUse("127.0.0.1:30020") {
		return statusFromState(state), Wrap(CodePortConflict, "start Apple Music login", errors.New("one or more required localhost ports are occupied"))
	}
	credentials := []byte(username + "\n" + password + "\n")
	if err := m.WSL.WritePrivateFile(ctx, state, LoginCredentialsLinuxPath, credentials); err != nil {
		clearBytes(credentials)
		return statusFromState(state), Wrap(CodeCommand, "stage private login credentials", err)
	}
	clearBytes(credentials)
	_ = m.WSL.RemovePrivateFile(ctx, state, LoginPendingLinuxPath)

	logPath, logFile, logOffset, err := m.openRuntimeLog("Apple Music login")
	if err != nil {
		_ = m.WSL.RemovePrivateFile(ctx, state, LoginCredentialsLinuxPath)
		return Status{}, err
	}
	process, err := m.WSL.StartLogin(state, logFile, logFile)
	if err != nil {
		logFile.Close()
		_ = m.WSL.RemovePrivateFile(ctx, state, LoginCredentialsLinuxPath)
		return statusFromState(state), Wrap(CodeCommand, "launch Apple Music login", err)
	}
	_ = process.Release()
	_ = logFile.Close()
	return m.waitForLogin(ctx, state, logPath, logOffset)
}

func (m *Manager) SubmitTwoFactorCode(ctx context.Context, code string) (Status, error) {
	if !validTwoFactorCode(code) {
		return Status{}, Wrap(CodeLoginFailed, "validate two-factor code", errors.New("the two-factor code must contain exactly six digits"))
	}
	unlock, err := m.Locker.Lock(ctx)
	if err != nil {
		return Status{}, Wrap(CodeConcurrentInstall, "lock runtime manager", err)
	}
	defer unlock()
	state, err := m.requireOwnedState(ctx)
	if err != nil {
		return Status{}, err
	}
	if runtimeHealthy(ctx) {
		return healthyStatus(state, ""), nil
	}
	pending, err := m.WSL.HasPendingLogin(ctx, state)
	if err != nil {
		return statusFromState(state), Wrap(CodeCommand, "check pending Apple Music login", err)
	}
	if !pending {
		return statusFromState(state), Wrap(CodeLoginFailed, "submit two-factor code", errors.New("no Apple Music login is waiting for a two-factor code"))
	}
	logPath := filepath.Join(m.Config.AppDataDir, "logs", "wrapper.log")
	logOffset := int64(0)
	if info, statErr := os.Stat(logPath); statErr == nil {
		logOffset = info.Size()
	}
	codeBytes := []byte(code + "\n")
	if err := m.WSL.WritePrivateFile(ctx, state, TwoFactorLinuxPath, codeBytes); err != nil {
		clearBytes(codeBytes)
		return statusFromState(state), Wrap(CodeCommand, "submit Apple Music two-factor code", err)
	}
	clearBytes(codeBytes)
	return m.waitForLogin(ctx, state, logPath, logOffset)
}

func (m *Manager) waitForLogin(ctx context.Context, state State, logPath string, logOffset int64) (Status, error) {
	deadline := time.Now().Add(m.Config.StartupTimeout)
	for time.Now().Before(deadline) {
		if runtimeHealthy(ctx) {
			_ = m.WSL.RemovePrivateFile(ctx, state, LoginPendingLinuxPath)
			return healthyStatus(state, logPath), nil
		}
		logText, _ := readLogFrom(logPath, logOffset)
		switch {
		case strings.Contains(logText, "[!] Waiting for input..."):
			if err := m.WSL.WritePrivateFile(ctx, state, LoginPendingLinuxPath, []byte("pending\n")); err != nil {
				return statusFromState(state), Wrap(CodeCommand, "record pending two-factor login", err)
			}
			status := statusFromState(state)
			status.Installed, status.Owned, status.Running = true, true, true
			status.LogPath = logPath
			return status, Wrap(CodeTwoFactorRequired, "complete Apple Music login", errors.New("a six-digit two-factor code is required; run submit-code while this login remains pending"))
		case strings.Contains(logText, "[!] login failed"), strings.Contains(logText, "Failed to get 2FA Code"):
			_ = m.WSL.RemovePrivateFile(ctx, state, LoginPendingLinuxPath)
			_ = m.WSL.Terminate(ctx, state.DistroName)
			return statusFromState(state), Wrap(CodeLoginFailed, "complete Apple Music login", fmt.Errorf("wrapper rejected the login; inspect %s", logPath))
		}
		select {
		case <-ctx.Done():
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = m.WSL.RemovePrivateFile(cleanupCtx, state, LoginPendingLinuxPath)
			_ = m.WSL.Terminate(cleanupCtx, state.DistroName)
			cancel()
			return Status{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	_ = m.WSL.RemovePrivateFile(ctx, state, LoginPendingLinuxPath)
	_ = m.WSL.Terminate(ctx, state.DistroName)
	return statusFromState(state), Wrap(CodeLoginFailed, "complete Apple Music login", fmt.Errorf("login timed out; inspect %s", logPath))
}

func (m *Manager) openRuntimeLog(operation string) (string, *os.File, int64, error) {
	logDir := filepath.Join(m.Config.AppDataDir, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return "", nil, 0, err
	}
	logPath := filepath.Join(logDir, "wrapper.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return "", nil, 0, err
	}
	if _, err := fmt.Fprintf(logFile, "\n--- %s %s ---\n", operation, m.Now().UTC().Format(time.RFC3339)); err != nil {
		logFile.Close()
		return "", nil, 0, err
	}
	info, err := logFile.Stat()
	if err != nil {
		logFile.Close()
		return "", nil, 0, err
	}
	return logPath, logFile, info.Size(), nil
}

func readLogFrom(name string, offset int64) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20))
	return string(data), err
}

func validateLoginCredentials(username, password string) error {
	if username == "" || password == "" {
		return errors.New("Apple ID and password must not be empty")
	}
	if strings.ContainsAny(username, ":\r\n\x00") || strings.ContainsAny(password, ":\r\n\x00") {
		return errors.New("the current wrapper login protocol does not support colons, newlines, or NUL characters in credentials")
	}
	return nil
}

func validTwoFactorCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func clearBytes(data []byte) {
	for index := range data {
		data[index] = 0
	}
}

func healthyStatus(state State, logPath string) Status {
	status := statusFromState(state)
	status.Installed, status.Owned, status.Running, status.Healthy = true, true, true, true
	status.LogPath = logPath
	return status
}

func (m *Manager) Stop(ctx context.Context) error {
	unlock, err := m.Locker.Lock(ctx)
	if err != nil {
		return Wrap(CodeConcurrentInstall, "lock runtime manager", err)
	}
	defer unlock()
	state, err := m.requireOwnedState(ctx)
	if err != nil {
		return err
	}
	return Wrap(CodeCommand, "stop managed runtime", m.WSL.Terminate(ctx, state.DistroName))
}

func (m *Manager) RemoveWithBackup(ctx context.Context, backupPath string) (string, error) {
	unlock, err := m.Locker.Lock(ctx)
	if err != nil {
		return "", Wrap(CodeConcurrentInstall, "lock runtime manager", err)
	}
	defer unlock()
	state, err := m.Store.Load()
	if errors.Is(err, os.ErrNotExist) || state.Stage == StageRemoved {
		return "", Wrap(CodeNotInstalled, "load managed runtime", errors.New("runtime is not installed"))
	}
	if err != nil {
		return "", err
	}
	if err := validateStateOwner(state); err != nil {
		return "", err
	}
	if err := validateManagedStateLayout(m.Config, state); err != nil {
		return "", Wrap(CodeRepairRequired, "validate managed runtime state", err)
	}
	if state.Stage == StageRemovalPrepared {
		if backupPath != "" {
			requested, pathErr := filepath.Abs(backupPath)
			if pathErr != nil {
				return "", pathErr
			}
			if !strings.EqualFold(filepath.Clean(requested), filepath.Clean(state.LastBackupPath)) {
				return "", Wrap(CodeRepairRequired, "resume runtime removal", fmt.Errorf("removal already has a committed backup at %s", state.LastBackupPath))
			}
		}
		return m.finishPreparedRemoval(ctx, state)
	}
	state, err = m.requireOwnedState(ctx)
	if err != nil {
		return "", err
	}
	if backupPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		backupDir := filepath.Join(home, "Documents", "AppleMusicDownloader Backups")
		if err := os.MkdirAll(backupDir, 0700); err != nil {
			return "", err
		}
		backupPath = filepath.Join(backupDir, fmt.Sprintf("%s-%s.tar", state.DistroName, m.Now().UTC().Format("20060102T150405Z")))
	}
	backupPath, err = filepath.Abs(backupPath)
	if err != nil {
		return "", err
	}
	if exists, err := pathExists(backupPath); err != nil {
		return "", err
	} else if exists {
		return "", fmt.Errorf("backup destination already exists: %s", backupPath)
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0700); err != nil {
		return "", err
	}
	if err := m.validateBackupPhysicalPath(state, backupPath, false); err != nil {
		return "", err
	}
	if err := m.WSL.Terminate(ctx, state.DistroName); err != nil {
		return "", Wrap(CodeCommand, "stop runtime before backup", err)
	}
	partialPath := backupPath + ".partial-" + state.InstanceID[:8] + "-" + m.Now().UTC().Format("20060102T150405Z")
	if exists, err := pathExists(partialPath); err != nil {
		return "", err
	} else if exists {
		return "", fmt.Errorf("partial backup destination already exists: %s", partialPath)
	}
	if err := m.WSL.Export(ctx, state.DistroName, partialPath); err != nil {
		return "", Wrap(CodeCommand, "back up runtime before removal", err)
	}
	backupInfo, err := os.Stat(partialPath)
	if err != nil {
		return "", Wrap(CodeCommand, "verify runtime backup", err)
	}
	if !backupInfo.Mode().IsRegular() || backupInfo.Size() == 0 {
		return "", Wrap(CodeCommand, "verify runtime backup", errors.New("WSL export did not produce a non-empty regular file"))
	}
	backupHash, backupSize, err := validateTarBackup(partialPath, state)
	if err != nil {
		return "", Wrap(CodeCommand, "validate runtime backup archive", err)
	}
	if err := m.validateBackupPhysicalPath(state, partialPath, true); err != nil {
		return "", Wrap(CodeRepairRequired, "validate exported backup location", err)
	}
	if err := moveFileNoReplace(partialPath, backupPath); err != nil {
		return "", Wrap(CodeCommand, "commit runtime backup", err)
	}
	if err := m.validateBackupPhysicalPath(state, backupPath, true); err != nil {
		return backupPath, Wrap(CodeRepairRequired, "validate committed backup location", err)
	}
	state.Stage = StageRemovalPrepared
	state.LastBackupPath = backupPath
	state.LastBackupSHA256 = backupHash
	state.LastBackupSize = backupSize
	if err := m.Store.Save(state); err != nil {
		return backupPath, err
	}
	return m.finishPreparedRemoval(ctx, state)
}

func (m *Manager) finishPreparedRemoval(ctx context.Context, state State) (string, error) {
	backupHash, backupSize, err := validateTarBackup(state.LastBackupPath, state)
	if err != nil {
		return state.LastBackupPath, Wrap(CodeRepairRequired, "revalidate committed runtime backup", err)
	}
	if !strings.EqualFold(backupHash, state.LastBackupSHA256) || backupSize != state.LastBackupSize {
		return state.LastBackupPath, Wrap(CodeRepairRequired, "revalidate committed runtime backup", errors.New("the committed backup changed after it was recorded"))
	}
	distros, err := m.WSL.List(ctx, false)
	if err != nil {
		return state.LastBackupPath, Wrap(CodeCommand, "list WSL distributions before removal", err)
	}
	if !containsDistro(distros, state.DistroName) {
		return m.commitRemovedState(state)
	}
	if err := m.validateBackupPhysicalPath(state, state.LastBackupPath, true); err != nil {
		return state.LastBackupPath, Wrap(CodeRepairRequired, "revalidate backup location before removal", err)
	}
	if err := m.WSL.VerifyOwner(ctx, state); err != nil {
		return state.LastBackupPath, Wrap(CodeOwnership, "reverify distribution before removal", err)
	}
	if err := m.WSL.Terminate(ctx, state.DistroName); err != nil {
		return state.LastBackupPath, Wrap(CodeCommand, "stop reverified runtime before removal", err)
	}
	if err := m.WSL.Unregister(ctx, state.DistroName); err != nil {
		return state.LastBackupPath, Wrap(CodeCommand, "remove managed distribution", err)
	}
	return m.commitRemovedState(state)
}

func (m *Manager) commitRemovedState(state State) (string, error) {
	state.Stage = StageRemoved
	state.RemovedAt = m.Now().UTC()
	if err := m.Store.Save(state); err != nil {
		return state.LastBackupPath, err
	}
	return state.LastBackupPath, nil
}

func validateTarBackup(name string, state State) (string, int64, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	tee := io.TeeReader(file, hash)
	reader := tar.NewReader(tee)
	buffer := make([]byte, 1<<20)
	entries := 0
	markerFound := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", 0, err
		}
		entries++
		entryName := archivepath.Clean(strings.TrimPrefix(strings.ReplaceAll(header.Name, "\\", "/"), "./"))
		if entryName == strings.TrimPrefix(MarkerLinuxPath, "/") {
			if markerFound {
				return "", 0, errors.New("backup tar contains duplicate ownership markers")
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || header.Size < 0 || header.Size > 64<<10 {
				return "", 0, errors.New("backup ownership marker is not a small regular file")
			}
			data, readErr := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
			if readErr != nil {
				return "", 0, readErr
			}
			var marker RuntimeMarker
			if err := json.Unmarshal(data, &marker); err != nil {
				return "", 0, fmt.Errorf("decode backup ownership marker: %w", err)
			}
			if err := validateRuntimeMarker(marker, state); err != nil {
				return "", 0, err
			}
			markerFound = true
		} else if _, err := io.CopyBuffer(io.Discard, reader, buffer); err != nil {
			return "", 0, err
		}
	}
	if entries == 0 {
		return "", 0, errors.New("backup tar contains no entries")
	}
	if !markerFound {
		return "", 0, errors.New("backup tar does not contain the managed runtime ownership marker")
	}
	if _, err := io.CopyBuffer(hash, file, buffer); err != nil {
		return "", 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func (m *Manager) validateBackupPhysicalPath(state State, candidate string, candidateExists bool) error {
	var resolvedCandidate string
	var err error
	if candidateExists {
		resolvedCandidate, err = canonicalExistingPath(candidate)
	} else {
		resolvedParent, resolveErr := canonicalExistingPath(filepath.Dir(candidate))
		if resolveErr != nil {
			return resolveErr
		}
		resolvedCandidate = filepath.Join(resolvedParent, filepath.Base(candidate))
	}
	if err != nil {
		return err
	}
	resolvedAppData, err := canonicalExistingPath(m.Config.AppDataDir)
	if err != nil {
		return err
	}
	inside, err := pathIsWithin(resolvedAppData, resolvedCandidate)
	if err != nil {
		return err
	}
	if inside {
		return fmt.Errorf("backup destination must be outside application-owned data directories: %s", m.Config.AppDataDir)
	}
	if exists, existsErr := pathExists(state.InstallDir); existsErr != nil {
		return existsErr
	} else if exists {
		resolvedInstall, resolveErr := canonicalExistingPath(state.InstallDir)
		if resolveErr != nil {
			return resolveErr
		}
		inside, relErr := pathIsWithin(resolvedInstall, resolvedCandidate)
		if relErr != nil {
			return relErr
		}
		if inside {
			return fmt.Errorf("backup destination must be outside the managed WSL install directory: %s", state.InstallDir)
		}
	}
	return nil
}

func pathIsWithin(parent, candidate string) (bool, error) {
	parentVolume := filepath.VolumeName(parent)
	candidateVolume := filepath.VolumeName(candidate)
	if parentVolume != "" && candidateVolume != "" && !strings.EqualFold(parentVolume, candidateVolume) {
		return false, nil
	}
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(candidate))
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)), nil
}

func (m *Manager) requireOwnedState(ctx context.Context) (State, error) {
	state, err := m.Store.Load()
	if errors.Is(err, os.ErrNotExist) || state.Stage == StageRemoved {
		return State{}, Wrap(CodeNotInstalled, "load managed runtime", errors.New("runtime is not installed"))
	}
	if err != nil {
		return State{}, err
	}
	if err := validateStateOwner(state); err != nil {
		return State{}, err
	}
	if err := validateManagedStateLayout(m.Config, state); err != nil {
		return State{}, Wrap(CodeRepairRequired, "validate managed runtime state", err)
	}
	distros, err := m.WSL.List(ctx, false)
	if err != nil {
		return State{}, err
	}
	if !containsDistro(distros, state.DistroName) {
		return State{}, Wrap(CodeNotInstalled, "locate managed runtime", errors.New("recorded distribution is not registered"))
	}
	if err := m.WSL.VerifyOwner(ctx, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func validateStateOwner(state State) error {
	currentSID, err := CurrentUserSID()
	if err != nil {
		return Wrap(CodeOwnership, "read current Windows identity", err)
	}
	if !strings.EqualFold(currentSID, state.OwnerSID) {
		return Wrap(CodeOwnership, "validate Windows identity", fmt.Errorf("runtime belongs to Windows SID %s, current process is %s", state.OwnerSID, currentSID))
	}
	return nil
}

func runtimeHealthy(ctx context.Context) bool {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:30020/", nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var account struct {
		Storefront string `json:"storefront_id"`
		DevToken   string `json:"dev_token"`
		MusicToken string `json:"music_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if decoder.Decode(&account) != nil || account.Storefront == "" || account.DevToken == "" || account.MusicToken == "" {
		return false
	}
	return portInUse("127.0.0.1:10020") && portInUse("127.0.0.1:20020")
}

func portInUse(address string) bool {
	connection, err := net.DialTimeout("tcp", address, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func statusFromState(state State) Status {
	return Status{
		Installed:      state.Stage == StageInstalled,
		Stage:          state.Stage,
		InstanceID:     state.InstanceID,
		DistroName:     state.DistroName,
		InstallDir:     state.InstallDir,
		RuntimeVersion: state.RuntimeVersion,
		RecoveryPaths:  append([]string(nil), state.RecoveryPaths...),
	}
}

func RedactLogLine(line string) string {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "music-token") || strings.Contains(lower, "dev_token") || strings.Contains(lower, "music_token") {
		return "[sensitive wrapper token redacted]"
	}
	return line
}
