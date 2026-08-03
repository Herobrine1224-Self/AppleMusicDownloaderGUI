package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type WSLClient struct {
	Runner  Runner
	WSLPath string
}

func (w WSLClient) PlatformReady(ctx context.Context) (bool, error) {
	result, err := w.Runner.Run(ctx, Command{Path: w.WSLPath, Args: []string{"--status"}})
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		return false, nil
	}
	// --status also succeeds on WSL1-only machines. The system distribution is
	// a WSL2 component and provides a side-effect-free capability probe that
	// never enters or changes a user's registered distributions.
	result, err = w.Runner.Run(ctx, Command{Path: w.WSLPath, Args: []string{"--system", "--exec", "/bin/true"}})
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

func (w WSLClient) List(ctx context.Context, runningOnly bool) ([]string, error) {
	args := []string{"--list"}
	if runningOnly {
		args = append(args, "--running")
	} else {
		args = append(args, "--all")
	}
	args = append(args, "--quiet")
	result, err := w.Runner.Run(ctx, Command{Path: w.WSLPath, Args: args})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandFailure("list WSL distributions", result)
	}
	return parseDistroList(result.Stdout), nil
}

func (w WSLClient) Import(ctx context.Context, name, installDir, archivePath string) error {
	result, err := w.Runner.Run(ctx, Command{
		Path:    w.WSLPath,
		Args:    []string{"--import", name, installDir, archivePath, "--version", "2"},
		Timeout: 30 * time.Minute,
	})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return commandFailure("import WSL distribution", result)
	}
	return nil
}

func (w WSLClient) Exec(ctx context.Context, name, user, linuxPath string, args ...string) (CommandResult, error) {
	return w.ExecInput(ctx, name, user, nil, linuxPath, args...)
}

func (w WSLClient) ExecInput(ctx context.Context, name, user string, stdin []byte, linuxPath string, args ...string) (CommandResult, error) {
	commandArgs := []string{"--distribution", name, "--user", user, "--exec", linuxPath}
	commandArgs = append(commandArgs, args...)
	result, err := w.Runner.Run(ctx, Command{Path: w.WSLPath, Args: commandArgs, Stdin: stdin})
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 {
		return result, commandFailure("execute command in managed WSL distribution", result)
	}
	return result, nil
}

func (w WSLClient) HasLoginState(ctx context.Context, state State) (bool, error) {
	script := `if [ -s "$1/STOREFRONT_ID" ] && [ -s "$1/MUSIC_TOKEN" ] && [ -d "$1/mpl_db" ]; then printf ready; else printf missing; fi`
	result, err := w.Exec(ctx, state.DistroName, "root", "/bin/sh", "-c", script, "applemusic-login-state", LoginDataLinuxDir)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(DecodeWindowsOutput(result.Stdout)) == "ready", nil
}

func (w WSLClient) HasPendingLogin(ctx context.Context, state State) (bool, error) {
	script := `if [ -f "$1" ]; then printf pending; else printf idle; fi`
	result, err := w.Exec(ctx, state.DistroName, "root", "/bin/sh", "-c", script, "applemusic-login-pending", LoginPendingLinuxPath)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(DecodeWindowsOutput(result.Stdout)) == "pending", nil
}

// ClearLoginData 删除 wrapper 保存的 Apple Music 登录数据（token、storefront、
// 曲库数据库和验证码），使下一次 start 返回 login_required。
func (w WSLClient) ClearLoginData(ctx context.Context, state State) error {
	script := `rm -rf -- "$1/STOREFRONT_ID" "$1/MUSIC_TOKEN" "$1/mpl_db" "$1/2fa.txt"`
	_, err := w.Exec(ctx, state.DistroName, "root", "/bin/sh", "-c", script, "applemusic-logout", LoginDataLinuxDir)
	return err
}

func (w WSLClient) WritePrivateFile(ctx context.Context, state State, linuxPath string, data []byte) error {
	script := `umask 077; tmp="$1.tmp.$$"; trap 'rm -f "$tmp"' EXIT HUP INT TERM; cat >"$tmp" && chmod 600 "$tmp" && mv -f "$tmp" "$1"`
	_, err := w.ExecInput(ctx, state.DistroName, "root", data, "/bin/sh", "-c", script, "applemusic-private-write", linuxPath)
	return err
}

func (w WSLClient) RemovePrivateFile(ctx context.Context, state State, linuxPath string) error {
	_, err := w.Exec(ctx, state.DistroName, "root", "/bin/rm", "-f", "--", linuxPath)
	return err
}

func (w WSLClient) Terminate(ctx context.Context, name string) error {
	result, err := w.Runner.Run(ctx, Command{Path: w.WSLPath, Args: []string{"--terminate", name}})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return commandFailure("terminate managed WSL distribution", result)
	}
	return nil
}

func (w WSLClient) Export(ctx context.Context, name, destination string) error {
	result, err := w.Runner.Run(ctx, Command{Path: w.WSLPath, Args: []string{"--export", name, destination}, Timeout: 6 * time.Hour})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return commandFailure("export managed WSL distribution", result)
	}
	return nil
}

func (w WSLClient) Unregister(ctx context.Context, name string) error {
	result, err := w.Runner.Run(ctx, Command{Path: w.WSLPath, Args: []string{"--unregister", name}})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return commandFailure("unregister managed WSL distribution", result)
	}
	return nil
}

func (w WSLClient) ReadMarker(ctx context.Context, state State) (RuntimeMarker, error) {
	result, err := w.Exec(ctx, state.DistroName, "root", "/usr/bin/cat", MarkerLinuxPath)
	if err != nil {
		return RuntimeMarker{}, err
	}
	var marker RuntimeMarker
	if err := json.Unmarshal([]byte(strings.TrimSpace(DecodeWindowsOutput(result.Stdout))), &marker); err != nil {
		return RuntimeMarker{}, fmt.Errorf("decode runtime ownership marker: %w", err)
	}
	return marker, nil
}

func (w WSLClient) VerifyOwner(ctx context.Context, state State) error {
	marker, err := w.ReadMarker(ctx, state)
	if err != nil {
		return Wrap(CodeOwnership, "read runtime marker", err)
	}
	if err := validateRuntimeMarker(marker, state); err != nil {
		return Wrap(CodeOwnership, "compare runtime marker", err)
	}
	return nil
}

func validateRuntimeMarker(marker RuntimeMarker, state State) error {
	if marker.ProductID != ProductID || marker.InstanceID != state.InstanceID || marker.RuntimeVersion != state.RuntimeVersion || marker.PayloadSHA256 != state.PayloadSHA256 || marker.UbuntuBaseSHA256 != state.UbuntuBaseSHA256 {
		return errors.New("the distribution marker does not match local installation state")
	}
	return nil
}

func (w WSLClient) SmokeTest(ctx context.Context, state State) error {
	result, err := w.Exec(ctx, state.DistroName, "root", RuntimeLinuxDir+"/run-wrapper", "--version")
	if err != nil {
		return err
	}
	output := DecodeWindowsOutput(append(append([]byte{}, result.Stdout...), result.Stderr...))
	if !strings.Contains(output, "1.2.0") {
		return fmt.Errorf("unexpected wrapper version output: %q", strings.TrimSpace(output))
	}
	result, err = w.Exec(ctx, state.DistroName, "root", "/usr/sbin/chroot", RuntimeLinuxDir+"/rootfs", "/system/bin/main", "--version")
	if err != nil {
		return fmt.Errorf("Android runtime loader smoke test: %w", err)
	}
	output = DecodeWindowsOutput(append(append([]byte{}, result.Stdout...), result.Stderr...))
	if !strings.Contains(output, "1.2.0") {
		return fmt.Errorf("unexpected Android runtime version output: %q", strings.TrimSpace(output))
	}
	return nil
}

func (w WSLClient) StartWrapper(state State, stdout, stderr interface{ Write([]byte) (int, error) }) (Process, error) {
	args := []string{"--distribution", state.DistroName, "--user", "root", "--exec", RuntimeLinuxDir + "/run-wrapper", "-H", "127.0.0.1"}
	return w.Runner.Start(Command{Path: w.WSLPath, Args: args, Dir: filepath.Dir(w.WSLPath)}, stdout, stderr)
}

func (w WSLClient) StartLogin(state State, stdout, stderr interface{ Write([]byte) (int, error) }) (Process, error) {
	script := `credentials="$1"
cleanup() { rm -f "$credentials"; }
trap cleanup EXIT HUP INT TERM
if [ ! -r "$credentials" ]; then exit 66; fi
{
  IFS= read -r username || exit 65
  IFS= read -r password || exit 65
} <"$credentials"
cleanup
trap - EXIT HUP INT TERM
if [ -z "$username" ] || [ -z "$password" ]; then exit 65; fi
exec "$2/run-wrapper" -L "$username:$password" -F -H 127.0.0.1
	`
	args := []string{"--distribution", state.DistroName, "--user", "root", "--exec", "/bin/sh", "-c", script, "applemusic-login", LoginCredentialsLinuxPath, RuntimeLinuxDir}
	return w.Runner.Start(Command{Path: w.WSLPath, Args: args, Dir: filepath.Dir(w.WSLPath)}, stdout, stderr)
}

func containsDistro(distros []string, target string) bool {
	for _, distro := range distros {
		if strings.EqualFold(strings.TrimSpace(distro), target) {
			return true
		}
	}
	return false
}

func commandFailure(operation string, result CommandResult) error {
	message := strings.TrimSpace(DecodeWindowsOutput(result.Stderr))
	if message == "" {
		message = strings.TrimSpace(DecodeWindowsOutput(result.Stdout))
	}
	if message == "" {
		message = "no command output"
	}
	return fmt.Errorf("%s failed with exit code %d: %s", operation, result.ExitCode, message)
}

func ensureLocalInstallPath(appDataDir, installDir string) error {
	base, err := filepath.Abs(appDataDir)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(installDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(base, target)
	if err != nil {
		return err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("managed WSL install path is outside application data directory")
	}
	return nil
}
