//go:build windows

package bootstrap

import (
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
		AppDataDir:        filepath.Join(localAppData, "AppleMusicDownloader"),
		PayloadDir:        payloadDir,
		UbuntuBasePath:    ubuntuBasePath,
		UbuntuBaseURL:     UbuntuBaseURL,
		UbuntuBaseMirrors: UbuntuBaseMirrors,
		UbuntuBaseHash:    UbuntuBaseSHA256,
		PayloadHash:       PayloadSHA256,
		RuntimeVersion:    RuntimeVersion,
		DownloadTimeout:   30 * 60 * 1e9,
		CommandTimeout:    15 * 60 * 1e9,
		StartupTimeout:    90 * 1e9,
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

// wslInstalledWithoutProbe reports whether the WSL optional component appears
// to be installed, without executing wsl.exe. When the WSL components are
// missing, the System32 wsl.exe launcher opens an interactive "install WSL"
// console window that blocks for up to a minute, so management commands must
// not invoke wsl.exe until a real installation is detected.
//
// The probe is deliberately conservative: it only returns true when a concrete
// WSL installation artifact is found (MSI metadata, Store package registration,
// WSL service, or WSL binaries). A false negative only makes the GUI show the
// deploy page, and install still performs the authoritative wsl.exe probe.
func wslInstalledWithoutProbe() bool {
	// WSL 2.x MSI (wsl --update / 本地 MSI) 把二进制装到 %ProgramFiles%\WSL，
	// 并在 Lxss\MSI 下登记安装元数据。
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		if _, err := os.Stat(filepath.Join(programFiles, "WSL", "wsl.exe")); err == nil {
			return true
		}
	}
	if windowsRegistryKeyExists(`SOFTWARE\Microsoft\Windows\CurrentVersion\Lxss\MSI`) {
		return true
	}
	// 经典可选组件注册 LxssManager 服务，现代 Store/MSI 版本注册 WslService
	// 服务；两者都通过 Windows 服务注册表登记，不必执行 wsl.exe。
	for _, service := range []string{"WslService", "LxssManager"} {
		if windowsRegistryKeyExists(`SYSTEM\CurrentControlSet\Services\` + service) {
			return true
		}
	}
	// Store/MSIX 安装会为所有用户登记 Windows Subsystem for Linux 包。
	return appxAllUsersPackageRegistered("MicrosoftCorporationII.WindowsSubsystemForLinux")
}

const (
	hkeyLocalMachine = uintptr(0x80000002)
	keyQueryValue    = uint32(0x0001)
	keyEnumSubKeys   = uint32(0x0008)
	errorSuccess     = uintptr(0)
)

var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyExW = advapi32.NewProc("RegOpenKeyExW")
	procRegCloseKey   = advapi32.NewProc("RegCloseKey")
	procRegEnumKeyExW = advapi32.NewProc("RegEnumKeyExW")
)

func windowsRegistryKeyExists(path string) bool {
	handle, ok := openWindowsRegistryKey(hkeyLocalMachine, path)
	if !ok {
		return false
	}
	procRegCloseKey.Call(handle)
	return true
}

func openWindowsRegistryKey(root uintptr, path string) (uintptr, bool) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var handle uintptr
	status, _, _ := procRegOpenKeyExW.Call(
		root,
		uintptr(unsafe.Pointer(name)),
		0,
		uintptr(keyQueryValue|keyEnumSubKeys),
		uintptr(unsafe.Pointer(&handle)),
	)
	if status != errorSuccess {
		return 0, false
	}
	return handle, true
}

func appxAllUsersPackageRegistered(packagePrefix string) bool {
	const path = `SOFTWARE\Microsoft\Windows\CurrentVersion\Appx\AppxAllUserStore\Applications`
	handle, ok := openWindowsRegistryKey(hkeyLocalMachine, path)
	if !ok {
		return false
	}
	defer procRegCloseKey.Call(handle)
	prefix := strings.ToLower(packagePrefix)
	for index := uint32(0); ; index++ {
		buffer := make([]uint16, 512)
		nameSize := uint32(len(buffer))
		status, _, _ := procRegEnumKeyExW.Call(
			handle,
			uintptr(index),
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(unsafe.Pointer(&nameSize)),
			0, 0, 0, 0,
		)
		if status != errorSuccess {
			return false
		}
		if strings.HasPrefix(strings.ToLower(syscall.UTF16ToString(buffer[:nameSize])), prefix) {
			return true
		}
	}
}
