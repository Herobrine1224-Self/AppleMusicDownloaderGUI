package bootstrap

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInstallCreatesDedicatedDistroWithoutTargetingUbuntu(t *testing.T) {
	manager, fake := newTestManager(t)
	status, err := manager.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Owned || !strings.HasPrefix(status.DistroName, DistroPrefix) {
		t.Fatalf("unexpected install status: %#v", status)
	}
	state, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != StageInstalled {
		t.Fatalf("state stage = %s, want %s", state.Stage, StageInstalled)
	}
	if fake.importCount != 1 || fake.terminateCount != 1 {
		t.Fatalf("import count = %d, terminate count = %d", fake.importCount, fake.terminateCount)
	}
	for _, call := range fake.callsSnapshot() {
		joined := strings.Join(call.Args, " ")
		if strings.Contains(joined, "--shutdown") || strings.Contains(joined, "--set-default") {
			t.Fatalf("unsafe global WSL command executed: %s", joined)
		}
		if targetsDistro(call.Args, "Ubuntu") {
			t.Fatalf("existing Ubuntu distribution was targeted: %s", joined)
		}
	}
}

func TestInstallIsIdempotentAndDoesNotTerminateInstalledRuntime(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	terminateCount := fake.terminateCount
	importCount := fake.importCount
	fake.running = true
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.importCount != importCount {
		t.Fatalf("second install imported again: %d -> %d", importCount, fake.importCount)
	}
	if fake.terminateCount != terminateCount {
		t.Fatalf("second install terminated runtime: %d -> %d", terminateCount, fake.terminateCount)
	}
}

func TestStatusDoesNotStartStoppedRuntime(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	execCount := fake.execCount
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Running {
		t.Fatal("Status() reported a stopped runtime as running")
	}
	if status.Owned {
		t.Fatal("Status() claimed ownership without checking the stopped runtime marker")
	}
	if fake.execCount != execCount {
		t.Fatalf("Status() entered a stopped distro: exec count %d -> %d", execCount, fake.execCount)
	}
}

func TestStopDoesNotStartStoppedRuntime(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.foreignMarker = true
	execCount := fake.execCount
	terminateCount := fake.terminateCount
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.running {
		t.Fatal("Stop() started a stopped runtime")
	}
	if fake.execCount != execCount {
		t.Fatalf("Stop() entered a stopped distro: exec count %d -> %d", execCount, fake.execCount)
	}
	if fake.terminateCount != terminateCount {
		t.Fatalf("Stop() terminated a stopped distro: terminate count %d -> %d", terminateCount, fake.terminateCount)
	}
}

func TestStopTerminatesOnlyRunningOwnedRuntime(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.running = true
	execCount := fake.execCount
	terminateCount := fake.terminateCount
	callCount := len(fake.callsSnapshot())
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.running {
		t.Fatal("Stop() left the managed runtime running")
	}
	if fake.execCount != execCount+1 {
		t.Fatalf("ownership checks = %d, want %d", fake.execCount, execCount+1)
	}
	if fake.terminateCount != terminateCount+1 {
		t.Fatalf("terminations = %d, want %d", fake.terminateCount, terminateCount+1)
	}
	for _, call := range fake.callsSnapshot()[callCount:] {
		joined := strings.Join(call.Args, " ")
		if strings.Contains(joined, "--shutdown") || targetsDistro(call.Args, "Ubuntu") {
			t.Fatalf("Stop() targeted WSL outside the managed runtime: %s", joined)
		}
	}
}

func TestStopRefusesRunningRuntimeWithForeignMarker(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.running = true
	fake.foreignMarker = true
	terminateCount := fake.terminateCount
	if err := manager.Stop(context.Background()); ErrorCodeOf(err) != CodeOwnership {
		t.Fatalf("Stop() error = %v, want CodeOwnership", err)
	}
	if fake.terminateCount != terminateCount {
		t.Fatalf("Stop() terminated an unowned runtime: %d -> %d", terminateCount, fake.terminateCount)
	}
	if !fake.running {
		t.Fatal("Stop() changed the state of an unowned runtime")
	}
}

func TestLogoutClearsLoginStateAndStopsRuntime(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.loginReady = true
	terminateCount := fake.terminateCount
	status, err := manager.Logout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Owned || status.Running || status.Healthy {
		t.Fatalf("unexpected logout status: %#v", status)
	}
	if fake.terminateCount != terminateCount+1 {
		t.Fatalf("logout did not stop the runtime: terminate %d -> %d", terminateCount, fake.terminateCount)
	}
	cleared := false
	for _, call := range fake.callsSnapshot() {
		if strings.Contains(strings.Join(call.Args, " "), "rm -rf") {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear Apple Music login data")
	}
}

func TestStatusDoesNotInvokeWSLWhenPlatformMissing(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := newAndSaveState(manager.Store, manager.Config, manager.Now()); err != nil {
		t.Fatal(err)
	}
	manager.platformProbe = func() bool { return false }
	status, err := manager.Status(context.Background())
	if ErrorCodeOf(err) != CodePlatform {
		t.Fatalf("Status() error = %v, want CodePlatform", err)
	}
	if status.Installed {
		t.Fatal("Status() reported an installed runtime without a WSL platform")
	}
	if got := len(fake.callsSnapshot()); got != 0 {
		t.Fatalf("Status() invoked wsl.exe %d time(s) while the platform is missing", got)
	}
}

func TestStopDoesNotInvokeWSLWhenPlatformMissing(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := newAndSaveState(manager.Store, manager.Config, manager.Now()); err != nil {
		t.Fatal(err)
	}
	manager.platformProbe = func() bool { return false }
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want success when there is no WSL platform", err)
	}
	if got := len(fake.callsSnapshot()); got != 0 {
		t.Fatalf("Stop() invoked wsl.exe %d time(s) while the platform is missing", got)
	}
}

func TestStatusDoesNotQueryRemovedDistroName(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RemoveWithBackup(context.Background(), filepath.Join(t.TempDir(), "runtime-backup.tar")); err != nil {
		t.Fatal(err)
	}
	callCount := len(fake.callsSnapshot())
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Installed || status.Stage != StageRemoved {
		t.Fatalf("unexpected removed status: %#v", status)
	}
	if got := len(fake.callsSnapshot()); got != callCount {
		t.Fatalf("Status() queried WSL after removal: calls %d -> %d", callCount, got)
	}
}

func TestStartReturnsLoginRequiredBeforeLaunchingWrapper(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Start(context.Background())
	if ErrorCodeOf(err) != CodeLoginRequired {
		t.Fatalf("Start() error = %v, want CodeLoginRequired", err)
	}
	if fake.startCount != 0 {
		t.Fatalf("Start() launched %d processes without login state", fake.startCount)
	}
}

func TestLoginKeepsCredentialsOutOfWSLCommandArguments(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.startOutput = "[!] Waiting for input...\n"
	username := "user@example.test"
	password := "correct-horse-battery"
	_, err := manager.Login(context.Background(), username, password)
	if ErrorCodeOf(err) != CodeTwoFactorRequired {
		t.Fatalf("Login() error = %v, want CodeTwoFactorRequired", err)
	}
	if !fake.loginPending || fake.startCount != 1 {
		t.Fatalf("login did not preserve its 2FA session: pending=%t starts=%d", fake.loginPending, fake.startCount)
	}
	for _, call := range fake.callsSnapshot() {
		joined := strings.Join(call.Args, " ")
		if strings.Contains(joined, username) || strings.Contains(joined, password) {
			t.Fatalf("credentials appeared in Windows command arguments: %s", joined)
		}
	}
	staged := string(fake.privateWrites[LoginCredentialsLinuxPath])
	if staged != username+"\n"+password+"\n" {
		t.Fatalf("credentials were not delivered through private stdin data: %q", staged)
	}
}

func TestInstallStopsBeforeDownloadWhenPlatformIsUnavailable(t *testing.T) {
	manager, fake := newTestManager(t)
	fake.platformReady = false
	_, err := manager.Install(context.Background())
	if ErrorCodeOf(err) != CodePlatform {
		t.Fatalf("Install() error = %v, want CodePlatform", err)
	}
	if fake.importCount != 0 {
		t.Fatalf("platform-unavailable install imported %d distributions", fake.importCount)
	}
	state, loadErr := manager.Store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.Stage != StagePlatformPending {
		t.Fatalf("state stage = %s, want %s", state.Stage, StagePlatformPending)
	}
}

func TestInstallRotatesStaleStateWhenBaseHashChangedBeforeRegistration(t *testing.T) {
	manager, fake := newTestManager(t)
	state, err := newAndSaveState(manager.Store, manager.Config, manager.Now())
	if err != nil {
		t.Fatal(err)
	}
	state.Stage = StagePlatformPending
	state.UbuntuBaseSHA256 = "old-ubuntu-base-hash"
	if err := manager.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	fake.platformReady = false
	_, err = manager.Install(context.Background())
	if ErrorCodeOf(err) != CodePlatform {
		t.Fatalf("Install() error = %v, want CodePlatform", err)
	}
	next, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if next.InstanceID == state.InstanceID {
		t.Fatal("stale pre-registration state was not rotated to a fresh instance")
	}
	if next.UbuntuBaseSHA256 != manager.Config.UbuntuBaseHash {
		t.Fatalf("rotated state base hash = %q, want %q", next.UbuntuBaseSHA256, manager.Config.UbuntuBaseHash)
	}
}

func TestInstallPreservesAndRecoversStaleInterruptedImport(t *testing.T) {
	manager, fake := newTestManager(t)
	state, err := newAndSaveState(manager.Store, manager.Config, manager.Now())
	if err != nil {
		t.Fatal(err)
	}
	state.Stage = StageRuntimeBuilt
	if err := manager.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	partialVHD := filepath.Join(state.InstallDir, "ext4.vhdx")
	if err := os.WriteFile(partialVHD, []byte("partial-import-data"), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := manager.Now().Add(-incompleteImportGracePeriod - time.Minute)
	if err := os.Chtimes(partialVHD, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(state.InstallDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	status, err := manager.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || fake.importCount != 1 || fake.unregisterCount != 0 {
		t.Fatalf("unexpected recovery install: status=%#v import=%d unregister=%d", status, fake.importCount, fake.unregisterCount)
	}
	next, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if next.InstanceID == state.InstanceID || len(next.RecoveryPaths) != 1 {
		t.Fatalf("interrupted import did not rotate state safely: %#v", next)
	}
	recoveryPath := next.RecoveryPaths[0]
	if _, err := os.Stat(filepath.Join(recoveryPath, "ext4.vhdx")); err != nil {
		t.Fatalf("preserved partial VHD is missing: %v", err)
	}
	if _, err := os.Stat(state.InstallDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old install path still exists after quarantine: %v", err)
	}
}

func TestInstallLeavesRecentInterruptedImportUntouched(t *testing.T) {
	manager, fake := newTestManager(t)
	state, err := newAndSaveState(manager.Store, manager.Config, manager.Now())
	if err != nil {
		t.Fatal(err)
	}
	state.Stage = StageRuntimeBuilt
	if err := manager.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(state.InstallDir, manager.Now(), manager.Now()); err != nil {
		t.Fatal(err)
	}

	_, err = manager.Install(context.Background())
	if ErrorCodeOf(err) != CodeRepairRequired {
		t.Fatalf("Install() error = %v, want CodeRepairRequired while import may still be active", err)
	}
	if fake.importCount != 0 || fake.unregisterCount != 0 {
		t.Fatalf("recent interrupted import was modified: import=%d unregister=%d", fake.importCount, fake.unregisterCount)
	}
	if _, err := os.Stat(state.InstallDir); err != nil {
		t.Fatalf("recent interrupted import was not preserved: %v", err)
	}
}

func TestInstallRecoversRenameCompletedBeforeStateSave(t *testing.T) {
	manager, fake := newTestManager(t)
	state, err := newAndSaveState(manager.Store, manager.Config, manager.Now())
	if err != nil {
		t.Fatal(err)
	}
	state.Stage = StageRuntimeBuilt
	if err := manager.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	recoveryPath := manager.incompleteRecoveryPath(state)
	if err := os.MkdirAll(recoveryPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recoveryPath, "ext4.vhdx"), []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}

	status, err := manager.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || fake.importCount != 1 {
		t.Fatalf("unexpected resumed recovery: %#v import=%d", status, fake.importCount)
	}
	next, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(next.RecoveryPaths) != 1 || !strings.EqualFold(next.RecoveryPaths[0], recoveryPath) {
		t.Fatalf("recovery path was not reconstructed: %#v", next.RecoveryPaths)
	}
}

func TestInstallRefusesForeignMarker(t *testing.T) {
	manager, fake := newTestManager(t)
	state, err := newAndSaveState(manager.Store, manager.Config, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fake.registered = true
	fake.distroName = state.DistroName
	fake.foreignMarker = true
	_, err = manager.Install(context.Background())
	if ErrorCodeOf(err) != CodeNameConflict {
		t.Fatalf("Install() error = %v, want CodeNameConflict", err)
	}
	if fake.importCount != 0 || fake.terminateCount != 0 || fake.unregisterCount != 0 {
		t.Fatalf("foreign distro was modified: import=%d terminate=%d unregister=%d", fake.importCount, fake.terminateCount, fake.unregisterCount)
	}
}

func TestRemoveBacksUpBeforeUnregister(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "runtime-backup.tar")
	got, err := manager.RemoveWithBackup(context.Background(), backup)
	if err != nil {
		t.Fatal(err)
	}
	if got != backup || fake.exportCount != 1 || fake.unregisterCount != 1 {
		t.Fatalf("remove result = %q export=%d unregister=%d", got, fake.exportCount, fake.unregisterCount)
	}
	if fake.exportSequence == 0 || fake.unregisterSequence <= fake.exportSequence {
		t.Fatalf("unregister happened before export: export=%d unregister=%d", fake.exportSequence, fake.unregisterSequence)
	}
	state, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != StageRemoved || state.LastBackupPath != backup {
		t.Fatalf("unexpected removed state: %#v", state)
	}
}

func TestRemoveRejectsBackupInsideManagedInstallDirectory(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.RemoveWithBackup(context.Background(), filepath.Join(state.InstallDir, "unsafe-backup.tar"))
	if err == nil {
		t.Fatal("RemoveWithBackup() accepted a backup inside the distribution install directory")
	}
	if fake.exportCount != 0 || fake.unregisterCount != 0 {
		t.Fatalf("unsafe backup path modified the distro: export=%d unregister=%d", fake.exportCount, fake.unregisterCount)
	}
}

func TestRemoveResumesCommittedBackupAfterUnregisterFailure(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "runtime-backup.tar")
	fake.unregisterFailures = 1
	if _, err := manager.RemoveWithBackup(context.Background(), backup); ErrorCodeOf(err) != CodeCommand {
		t.Fatalf("first RemoveWithBackup() error = %v, want CodeCommand", err)
	}
	prepared, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Stage != StageRemovalPrepared || prepared.LastBackupPath != backup || prepared.LastBackupSHA256 == "" {
		t.Fatalf("backup was not committed before unregister: %#v", prepared)
	}
	if _, err := manager.RemoveWithBackup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	if fake.exportCount != 1 || fake.unregisterCount != 2 {
		t.Fatalf("resume did not reuse backup: export=%d unregister=%d", fake.exportCount, fake.unregisterCount)
	}
}

func TestRemoveRejectsBackupWithForeignOwnershipMarker(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "runtime-backup.tar")
	fake.foreignExportMarker = true
	if _, err := manager.RemoveWithBackup(context.Background(), backup); err == nil {
		t.Fatal("RemoveWithBackup() accepted a backup from a foreign runtime")
	}
	if fake.unregisterCount != 0 {
		t.Fatalf("foreign backup caused %d unregister calls", fake.unregisterCount)
	}
	state, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != StageInstalled {
		t.Fatalf("foreign backup changed removal stage to %s", state.Stage)
	}
}

func TestRemoveDoesNotOverwriteTargetCreatedDuringExport(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "runtime-backup.tar")
	fake.createBackupTargetDuringExport = true
	if _, err := manager.RemoveWithBackup(context.Background(), backup); err == nil {
		t.Fatal("RemoveWithBackup() overwrote a destination created during export")
	}
	contents, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "concurrent-owner" {
		t.Fatalf("concurrent target was overwritten: %q", contents)
	}
	if fake.unregisterCount != 0 {
		t.Fatalf("failed backup commit caused %d unregister calls", fake.unregisterCount)
	}
}

func TestRemoveFinishesStateAfterPriorUnregister(t *testing.T) {
	manager, fake := newTestManager(t)
	if _, err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "runtime-backup.tar")
	if err := fake.writeFakeExport(backup); err != nil {
		t.Fatal(err)
	}
	hash, size, err := validateTarBackup(backup, state)
	if err != nil {
		t.Fatal(err)
	}
	state.Stage = StageRemovalPrepared
	state.LastBackupPath = backup
	state.LastBackupSHA256 = hash
	state.LastBackupSize = size
	if err := manager.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	fake.registered = false
	unregisterCount := fake.unregisterCount
	if _, err := manager.RemoveWithBackup(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if fake.unregisterCount != unregisterCount {
		t.Fatal("resume attempted to unregister an already absent distribution")
	}
	removed, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if removed.Stage != StageRemoved {
		t.Fatalf("resumed removal stage = %s, want %s", removed.Stage, StageRemoved)
	}
}

func TestWSLClientPreservesArgumentsAsSeparateValues(t *testing.T) {
	runner := &recordingRunner{responses: []CommandResult{{ExitCode: 0}}}
	client := WSLClient{Runner: runner, WSLPath: `C:\Windows\System32\wsl.exe`}
	err := client.Import(context.Background(), "AppleMusic-Runtime-aabbccdd", `C:\Users\Test User\App Data\runtime`, `C:\路径\runtime image.tar`)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("got %d calls", len(runner.calls))
	}
	want := []string{"--import", "AppleMusic-Runtime-aabbccdd", `C:\Users\Test User\App Data\runtime`, `C:\路径\runtime image.tar`, "--version", "2"}
	if strings.Join(runner.calls[0].Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", runner.calls[0].Args, want)
	}
}

func targetsDistro(args []string, name string) bool {
	for i, arg := range args {
		if (arg == "--distribution" || arg == "-d" || arg == "--terminate" || arg == "--unregister" || arg == "--export") && i+1 < len(args) && strings.EqualFold(args[i+1], name) {
			return true
		}
	}
	return false
}

func newTestManager(t *testing.T) (*Manager, *simulatedWSLRunner) {
	t.Helper()
	work := t.TempDir()
	payload := makeTestPayload(t, work)
	base := filepath.Join(work, "base.tar.gz")
	writeTestBase(t, base, false)
	payloadHash, err := HashPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(work, payload, base, payloadHash, hashFileForTest(t, base))
	store := StateStore{Path: filepath.Join(work, "bootstrap-state.json")}
	fake := &simulatedWSLRunner{platformReady: true, store: store}
	wsl := WSLClient{Runner: fake, WSLPath: `C:\Windows\System32\wsl.exe`}
	manager := &Manager{
		Config:        config,
		Runner:        fake,
		WSL:           wsl,
		Artifacts:     ArtifactManager{Config: config},
		Store:         store,
		Locker:        immediateLock{},
		Now:           func() time.Time { return time.Unix(1700000000, 0) },
		healthProbe:   func(context.Context) bool { return false },
		portProbe:     func(string) bool { return false },
		platformProbe: func() bool { return true },
	}
	return manager, fake
}

type simulatedWSLRunner struct {
	mu                             sync.Mutex
	store                          StateStore
	platformReady                  bool
	registered                     bool
	running                        bool
	foreignMarker                  bool
	distroName                     string
	calls                          []Command
	sequence                       int
	importCount                    int
	terminateCount                 int
	exportCount                    int
	unregisterCount                int
	exportSequence                 int
	unregisterSequence             int
	execCount                      int
	unregisterFailures             int
	foreignExportMarker            bool
	createBackupTargetDuringExport bool
	loginReady                     bool
	loginPending                   bool
	privateWrites                  map[string][]byte
	startOutput                    string
	startCount                     int
}

func (f *simulatedWSLRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, command)
	f.sequence++
	args := command.Args
	if len(args) == 0 {
		return CommandResult{ExitCode: 1}, nil
	}
	switch args[0] {
	case "--status":
		if f.platformReady {
			return CommandResult{ExitCode: 0}, nil
		}
		return CommandResult{ExitCode: 1, Stderr: utf16Bytes("WSL unavailable", false)}, nil
	case "--system":
		if f.platformReady {
			return CommandResult{ExitCode: 0}, nil
		}
		return CommandResult{ExitCode: 1}, nil
	case "--list":
		lines := []string{"Ubuntu"}
		if f.registered {
			lines = append(lines, f.distroName)
		}
		if containsArg(args, "--running") && !f.running {
			lines = nil
		}
		return CommandResult{ExitCode: 0, Stdout: utf16Bytes(strings.Join(lines, "\r\n")+"\r\n", true)}, nil
	case "--import":
		f.importCount++
		f.registered = true
		f.distroName = args[1]
		return CommandResult{ExitCode: 0}, nil
	case "--distribution":
		f.execCount++
		f.running = true
		return f.execCommand(command)
	case "--terminate":
		f.terminateCount++
		f.running = false
		return CommandResult{ExitCode: 0}, nil
	case "--export":
		f.exportCount++
		f.exportSequence = f.sequence
		if err := f.writeFakeExport(args[2]); err != nil {
			return CommandResult{}, err
		}
		if f.createBackupTargetDuringExport {
			finalPath := strings.SplitN(args[2], ".partial-", 2)[0]
			if err := os.WriteFile(finalPath, []byte("concurrent-owner"), 0600); err != nil {
				return CommandResult{}, err
			}
		}
		return CommandResult{ExitCode: 0}, nil
	case "--unregister":
		f.unregisterCount++
		f.unregisterSequence = f.sequence
		if f.unregisterFailures > 0 {
			f.unregisterFailures--
			return CommandResult{ExitCode: 1, Stderr: []byte("simulated unregister failure")}, nil
		}
		f.registered = false
		return CommandResult{ExitCode: 0}, nil
	default:
		return CommandResult{ExitCode: 1, Stderr: []byte("unexpected command")}, nil
	}
}

func (f *simulatedWSLRunner) writeFakeExport(name string) error {
	file, err := os.Create(name)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(file)
	state, err := f.store.Load()
	if err != nil {
		file.Close()
		return err
	}
	marker := RuntimeMarker{
		ProductID:        ProductID,
		InstanceID:       state.InstanceID,
		RuntimeVersion:   state.RuntimeVersion,
		PayloadSHA256:    state.PayloadSHA256,
		UbuntuBaseSHA256: state.UbuntuBaseSHA256,
	}
	if f.foreignExportMarker {
		marker.InstanceID = "foreign-instance"
	}
	data, err := json.Marshal(marker)
	if err != nil {
		file.Close()
		return err
	}
	if err := writer.WriteHeader(&tar.Header{Name: "etc/applemusic-runtime.json", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(data))}); err != nil {
		file.Close()
		return err
	}
	if _, err := writer.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (f *simulatedWSLRunner) execCommand(command Command) (CommandResult, error) {
	args := command.Args
	execIndex := indexOf(args, "--exec")
	if execIndex < 0 || execIndex+1 >= len(args) {
		return CommandResult{ExitCode: 1}, nil
	}
	linuxCommand := args[execIndex+1]
	switch linuxCommand {
	case "/usr/bin/cat":
		state, err := f.store.Load()
		if err != nil {
			return CommandResult{}, err
		}
		marker := RuntimeMarker{
			ProductID:        ProductID,
			InstanceID:       state.InstanceID,
			RuntimeVersion:   state.RuntimeVersion,
			PayloadSHA256:    state.PayloadSHA256,
			UbuntuBaseSHA256: state.UbuntuBaseSHA256,
		}
		if f.foreignMarker {
			marker.InstanceID = "foreign-instance"
		}
		data, _ := json.Marshal(marker)
		return CommandResult{ExitCode: 0, Stdout: data}, nil
	case "/usr/bin/id":
		return CommandResult{ExitCode: 1, Stderr: []byte("unknown user")}, nil
	case "/usr/sbin/useradd":
		return CommandResult{ExitCode: 0}, nil
	case RuntimeLinuxDir + "/run-wrapper":
		return CommandResult{ExitCode: 0, Stdout: []byte("wrapper 1.2.0\n")}, nil
	case "/usr/sbin/chroot":
		return CommandResult{ExitCode: 0, Stdout: []byte("wrapper 1.2.0\n")}, nil
	case "/bin/sh":
		script := strings.Join(args[execIndex+2:], " ")
		switch {
		case strings.Contains(script, "rm -rf"):
			return CommandResult{ExitCode: 0}, nil
		case strings.Contains(script, "STOREFRONT_ID"):
			if f.loginReady {
				return CommandResult{ExitCode: 0, Stdout: []byte("ready")}, nil
			}
			return CommandResult{ExitCode: 0, Stdout: []byte("missing")}, nil
		case strings.Contains(script, "printf pending"):
			if f.loginPending {
				return CommandResult{ExitCode: 0, Stdout: []byte("pending")}, nil
			}
			return CommandResult{ExitCode: 0, Stdout: []byte("idle")}, nil
		case strings.Contains(script, "tmp=\"$1.tmp.$$"):
			linuxPath := args[len(args)-1]
			if f.privateWrites == nil {
				f.privateWrites = make(map[string][]byte)
			}
			f.privateWrites[linuxPath] = append([]byte(nil), command.Stdin...)
			if linuxPath == LoginPendingLinuxPath {
				f.loginPending = true
			}
			return CommandResult{ExitCode: 0}, nil
		default:
			return CommandResult{ExitCode: 1, Stderr: []byte("unexpected shell command")}, nil
		}
	case "/bin/rm":
		linuxPath := args[len(args)-1]
		if linuxPath == LoginPendingLinuxPath {
			f.loginPending = false
		}
		return CommandResult{ExitCode: 0}, nil
	default:
		return CommandResult{ExitCode: 1, Stderr: []byte("unexpected linux command")}, nil
	}
}

func (f *simulatedWSLRunner) Start(command Command, stdout, stderr io.Writer) (Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, command)
	f.startCount++
	f.running = true
	if f.startOutput != "" {
		_, _ = io.WriteString(stderr, f.startOutput)
	}
	return fakeProcess{}, nil
}

func (f *simulatedWSLRunner) callsSnapshot() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.calls...)
}

type recordingRunner struct {
	calls     []Command
	responses []CommandResult
}

func (r *recordingRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	r.calls = append(r.calls, command)
	if len(r.responses) == 0 {
		return CommandResult{}, nil
	}
	result := r.responses[0]
	r.responses = r.responses[1:]
	return result, nil
}

func (r *recordingRunner) Start(command Command, stdout, stderr io.Writer) (Process, error) {
	r.calls = append(r.calls, command)
	return fakeProcess{}, nil
}

type fakeProcess struct{}

func (fakeProcess) PID() int       { return 1234 }
func (fakeProcess) Release() error { return nil }

func containsArg(args []string, value string) bool { return indexOf(args, value) >= 0 }

func indexOf(args []string, value string) int {
	for index, arg := range args {
		if arg == value {
			return index
		}
	}
	return -1
}

func TestStateStoreRejectsForeignState(t *testing.T) {
	store := StateStore{Path: filepath.Join(t.TempDir(), "state.json")}
	data := []byte(`{"schema_version":1,"product_id":"other","instance_id":"x","distro_name":"Ubuntu"}`)
	if err := os.WriteFile(store.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted foreign state")
	}
}
