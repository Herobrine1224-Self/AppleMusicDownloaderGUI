//go:build windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

func AcquireSingleInstance() (release func(), acquired bool, err error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, false, err
	}
	return acquireNamedInstance(instanceMutexName(user.User.Sid.String()))
}

func instanceMutexName(userSID string) string {
	digest := sha256.Sum256([]byte(strings.ToUpper(userSID)))
	return `Global\AppleMusicDownloader.GUI.` + hex.EncodeToString(digest[:8])
}

func acquireNamedInstance(name string) (release func(), acquired bool, err error) {
	nameUTF16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, false, nameUTF16)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, false, nil
	}
	if err != nil {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, false, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = windows.CloseHandle(handle) })
	}, true, nil
}
