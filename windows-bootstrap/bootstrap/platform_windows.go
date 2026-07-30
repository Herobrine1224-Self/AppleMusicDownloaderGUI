//go:build windows

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const ExitRebootRequired = 20

func DefaultConfig(payloadDir, ubuntuBasePath string) (Config, error) {
	return defaultConfig(payloadDir, ubuntuBasePath, true)
}

func DefaultManagementConfig() (Config, error) {
	return defaultConfig("", "", false)
}

func defaultConfig(payloadDir, ubuntuBasePath string, requirePayload bool) (Config, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return Config{}, errors.New("LOCALAPPDATA is not set")
	}
	if requirePayload && payloadDir == "" {
		var err error
		payloadDir, err = findPayloadDir()
		if err != nil {
			return Config{}, err
		}
	}
	return Config{
		AppDataDir:      filepath.Join(localAppData, "AppleMusicDownloader"),
		PayloadDir:      payloadDir,
		UbuntuBasePath:  ubuntuBasePath,
		UbuntuBaseURL:   UbuntuBaseURL,
		UbuntuBaseHash:  UbuntuBaseSHA256,
		PayloadHash:     PayloadSHA256,
		RuntimeVersion:  RuntimeVersion,
		DownloadTimeout: 30 * 60 * 1e9,
		CommandTimeout:  15 * 60 * 1e9,
		StartupTimeout:  90 * 1e9,
	}, nil
}

func findPayloadDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	executableDir := filepath.Dir(executable)
	workingDir, _ := os.Getwd()
	candidates := []string{
		filepath.Join(executableDir, "payload"),
		filepath.Join(executableDir, "..", "wrapper-main"),
		filepath.Join(workingDir, "payload"),
		filepath.Join(workingDir, "wrapper-main"),
		filepath.Join(workingDir, "..", "wrapper-main"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "wrapper")); err == nil {
			if _, err := os.Stat(filepath.Join(candidate, "rootfs", "system", "bin", "main")); err == nil {
				return filepath.Clean(candidate), nil
			}
		}
	}
	return "", errors.New("wrapper payload was not found; pass --payload with the directory containing wrapper and rootfs")
}

func ValidateNativePlatform() error {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return Wrap(CodeUnsupported, "validate platform", fmt.Errorf("this runtime currently supports Windows amd64 only, got %s/%s", runtime.GOOS, runtime.GOARCH))
	}
	major, build, err := nativeWindowsVersion()
	if err != nil {
		return Wrap(CodeUnsupported, "read Windows version", err)
	}
	if major < 10 || (major == 10 && build < 19041) {
		return Wrap(CodeUnsupported, "validate Windows version", fmt.Errorf("Windows 10 build 19041 or newer is required, got major %d build %d", major, build))
	}
	architecture, err := nativeProcessorArchitecture()
	if err != nil {
		return Wrap(CodeUnsupported, "read native processor architecture", err)
	}
	const processorArchitectureAMD64 = 9
	if architecture != processorArchitectureAMD64 {
		return Wrap(CodeUnsupported, "validate platform", fmt.Errorf("the bundled wrapper requires native AMD64 Windows, processor architecture code is %d", architecture))
	}
	return nil
}

func CurrentUserSID() (string, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String()
}

type rtlOSVersionInfo struct {
	Size        uint32
	Major       uint32
	Minor       uint32
	Build       uint32
	PlatformID  uint32
	ServicePack [128]uint16
}

func nativeWindowsVersion() (uint32, uint32, error) {
	info := rtlOSVersionInfo{Size: uint32(unsafe.Sizeof(rtlOSVersionInfo{}))}
	status, _, callErr := syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion").Call(uintptr(unsafe.Pointer(&info)))
	if status != 0 {
		return 0, 0, callErr
	}
	return info.Major, info.Build, nil
}

type nativeSystemInfo struct {
	ProcessorArchitecture uint16
	Reserved              uint16
	PageSize              uint32
	MinimumAddress        uintptr
	MaximumAddress        uintptr
	ActiveProcessorMask   uintptr
	NumberOfProcessors    uint32
	ProcessorType         uint32
	AllocationGranularity uint32
	ProcessorLevel        uint16
	ProcessorRevision     uint16
}

func nativeProcessorArchitecture() (uint16, error) {
	var info nativeSystemInfo
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetNativeSystemInfo")
	proc.Call(uintptr(unsafe.Pointer(&info)))
	if info.ProcessorArchitecture == 0xffff {
		return 0, errors.New("GetNativeSystemInfo returned an unknown architecture")
	}
	return info.ProcessorArchitecture, nil
}

func SystemExecutable(name string) (string, error) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		return "", errors.New("SystemRoot is not set")
	}
	path := filepath.Join(root, "System32", name)
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

func EnableWSLFeatures(ctx context.Context, runner Runner) (bool, error) {
	if !IsElevated() {
		return false, errors.New("platform helper must run elevated")
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		return false, errors.New("SystemRoot is not set")
	}
	return enableWSLFeaturesAt(ctx, runner, root)
}

func enableWSLFeaturesAt(ctx context.Context, runner Runner, systemRoot string) (bool, error) {
	wslPath := filepath.Join(systemRoot, "System32", "wsl.exe")
	installSucceeded := false
	rebootRequired := false
	installResult, installErr := runner.Run(ctx, Command{
		Path: wslPath,
		Args: []string{"--install", "--no-distribution", "--web-download"},
	})
	if installErr == nil {
		switch installResult.ExitCode {
		case 0:
			installSucceeded = true
		case 3010:
			installSucceeded = true
			rebootRequired = true
		}
	}
	dism := filepath.Join(systemRoot, "System32", "dism.exe")
	features := []string{"Microsoft-Windows-Subsystem-Linux", "VirtualMachinePlatform"}
	for _, feature := range features {
		result, err := runner.Run(ctx, Command{Path: dism, Args: []string{
			"/Online", "/Enable-Feature", "/FeatureName:" + feature, "/All", "/NoRestart", "/English",
		}})
		if err != nil {
			return rebootRequired, err
		}
		if result.ExitCode == 3010 {
			rebootRequired = true
			continue
		}
		if result.ExitCode != 0 {
			return rebootRequired, commandFailure("enable Windows feature "+feature, result)
		}
		output := DecodeWindowsOutput(append(append([]byte{}, result.Stdout...), result.Stderr...))
		if strings.Contains(strings.ToLower(output), "restart required : yes") {
			rebootRequired = true
		}
	}
	if rebootRequired {
		return true, nil
	}
	if installSucceeded {
		return false, nil
	}

	// Older inbox WSL versions may not understand --no-distribution. Once the
	// optional components are enabled, update the kernel/package without ever
	// requesting a Store distribution installation.
	var updateErrors []error
	for _, args := range [][]string{{"--update", "--web-download"}, {"--update"}} {
		result, runErr := runner.Run(ctx, Command{Path: wslPath, Args: args})
		if runErr != nil {
			updateErrors = append(updateErrors, runErr)
			continue
		}
		if result.ExitCode == 0 {
			return false, nil
		}
		if result.ExitCode == 3010 {
			return true, nil
		}
		updateErrors = append(updateErrors, commandFailure("update WSL2 kernel", result))
	}
	status, statusErr := runner.Run(ctx, Command{Path: wslPath, Args: []string{"--status"}})
	if statusErr == nil && status.ExitCode == 0 {
		return false, nil
	}
	if installErr != nil {
		updateErrors = append(updateErrors, installErr)
	} else if installResult.ExitCode != 0 {
		updateErrors = append(updateErrors, commandFailure("install WSL platform", installResult))
	}
	if statusErr != nil {
		updateErrors = append(updateErrors, statusErr)
	} else if status.ExitCode != 0 {
		updateErrors = append(updateErrors, commandFailure("probe WSL platform after enable", status))
	}
	return false, errors.Join(updateErrors...)
}
