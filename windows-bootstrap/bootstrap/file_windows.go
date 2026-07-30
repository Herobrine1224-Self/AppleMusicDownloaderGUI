//go:build windows

package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func replaceFile(source, destination string) error {
	src, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	dst, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	const moveFileReplaceExisting = 0x1
	moveFileEx := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
	ok, _, callErr := moveFileEx.Call(
		uintptr(unsafe.Pointer(src)),
		uintptr(unsafe.Pointer(dst)),
		moveFileReplaceExisting,
	)
	if ok == 0 {
		return callErr
	}
	return nil
}

func moveFileNoReplace(source, destination string) error {
	src, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	dst, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	moveFileEx := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
	ok, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(dst)), 0)
	if ok == 0 {
		return callErr
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isReparsePoint(path string) (bool, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := syscall.GetFileAttributes(name)
	if err != nil {
		return false, err
	}
	const fileAttributeReparsePoint = 0x400
	return attributes&fileAttributeReparsePoint != 0, nil
}

func canonicalExistingPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")
	buffer := make([]uint16, 512)
	for {
		length, _, callErr := proc.Call(file.Fd(), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if length == 0 {
			if callErr != nil && !errors.Is(callErr, syscall.Errno(0)) {
				return "", callErr
			}
			return "", errors.New("GetFinalPathNameByHandleW failed")
		}
		if length < uintptr(len(buffer)) {
			resolved := syscall.UTF16ToString(buffer[:length])
			return normalizeFinalWindowsPath(resolved), nil
		}
		if length > 32767 {
			return "", fmt.Errorf("resolved Windows path is unexpectedly long: %d", length)
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func normalizeFinalWindowsPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}
