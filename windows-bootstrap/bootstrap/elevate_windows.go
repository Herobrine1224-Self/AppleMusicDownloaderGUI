//go:build windows

package bootstrap

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	procIsUserAnAdmin       = shell32.NewProc("IsUserAnAdmin")
	procShellExecuteExW     = shell32.NewProc("ShellExecuteExW")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procWaitForSingleObject = kernel32.NewProc("WaitForSingleObject")
	procGetExitCodeProcess  = kernel32.NewProc("GetExitCodeProcess")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
)

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Hwnd       uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   uintptr
	IDList     uintptr
	Class      *uint16
	ClassKey   uintptr
	HotKey     uint32
	Icon       uintptr
	Process    uintptr
}

func IsElevated() bool {
	result, _, _ := procIsUserAnAdmin.Call()
	return result != 0
}

func RunElevated(executable string, args []string) (int, error) {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return -1, err
	}
	parameters, err := syscall.UTF16PtrFromString(makeCommandLine(args))
	if err != nil {
		return -1, err
	}
	directory, err := syscall.UTF16PtrFromString(mustWorkingDirectory())
	if err != nil {
		return -1, err
	}
	const (
		seeMaskNoCloseProcess = 0x00000040
		seeMaskNoAsync        = 0x00000100
		infinite              = 0xffffffff
	)
	info := shellExecuteInfo{
		Size:       uint32(unsafe.Sizeof(shellExecuteInfo{})),
		Mask:       seeMaskNoCloseProcess | seeMaskNoAsync,
		Verb:       verb,
		File:       file,
		Parameters: parameters,
		Directory:  directory,
		Show:       0,
	}
	ok, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if callErr != nil && !errors.Is(callErr, syscall.Errno(0)) {
			return -1, callErr
		}
		return -1, errors.New("UAC elevation was cancelled or failed")
	}
	if info.Process == 0 {
		return -1, errors.New("elevated helper did not return a process handle")
	}
	defer procCloseHandle.Call(info.Process)
	waitResult, _, waitErr := procWaitForSingleObject.Call(info.Process, infinite)
	if waitResult == 0xffffffff {
		return -1, waitErr
	}
	var exitCode uint32
	ok, _, exitErr := procGetExitCodeProcess.Call(info.Process, uintptr(unsafe.Pointer(&exitCode)))
	if ok == 0 {
		return -1, exitErr
	}
	return int(exitCode), nil
}

func makeCommandLine(args []string) string {
	line := ""
	for i, arg := range args {
		if i > 0 {
			line += " "
		}
		line += syscall.EscapeArg(arg)
	}
	return line
}

func mustWorkingDirectory() string {
	directory, err := os.Getwd()
	if err != nil {
		return ""
	}
	return directory
}
