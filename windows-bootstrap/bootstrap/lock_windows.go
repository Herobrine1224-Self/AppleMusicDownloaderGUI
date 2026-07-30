//go:build windows

package bootstrap

import (
	"context"
	"errors"
	"syscall"
	"time"
	"unsafe"
)

type NamedMutex struct {
	Name string
}

func (m NamedMutex) Lock(ctx context.Context) (func(), error) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel.NewProc("CreateMutexW")
	waitForSingleObject := kernel.NewProc("WaitForSingleObject")
	releaseMutex := kernel.NewProc("ReleaseMutex")
	closeHandle := kernel.NewProc("CloseHandle")
	name, err := syscall.UTF16PtrFromString(m.Name)
	if err != nil {
		return nil, err
	}
	handle, _, createErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, createErr
	}
	for {
		result, _, waitErr := waitForSingleObject.Call(handle, 200)
		switch result {
		case 0, 0x80:
			return func() {
				releaseMutex.Call(handle)
				closeHandle.Call(handle)
			}, nil
		case 0x102:
			select {
			case <-ctx.Done():
				closeHandle.Call(handle)
				return nil, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		default:
			closeHandle.Call(handle)
			if waitErr != nil && !errors.Is(waitErr, syscall.Errno(0)) {
				return nil, waitErr
			}
			return nil, errors.New("failed to acquire bootstrap mutex")
		}
	}
}
