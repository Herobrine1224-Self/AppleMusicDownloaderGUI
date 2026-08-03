//go:build !windows

package bootstrap

import (
	"errors"
)

const ExitRebootRequired = 20

func DefaultConfig(payloadDir, ubuntuBasePath string) (Config, error) {
	return Config{}, errors.New("WSL bootstrap is only supported on Windows")
}
func DefaultManagementConfig() (Config, error) {
	return Config{}, errors.New("WSL bootstrap is only supported on Windows")
}
func ValidateNativePlatform() error                { return errors.New("WSL bootstrap is only supported on Windows") }
func CurrentUserSID() (string, error)              { return "", errors.New("unsupported platform") }
func SystemExecutable(name string) (string, error) { return "", errors.New("unsupported platform") }
func wslInstalledWithoutProbe() bool               { return true }
