//go:build !windows

package bootstrap

import (
	"context"
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
func EnableWSLFeatures(ctx context.Context, runner Runner) (bool, error) {
	return false, errors.New("unsupported platform")
}
func IsElevated() bool { return false }
func RunElevated(executable string, args []string) (int, error) {
	return -1, errors.New("unsupported platform")
}
