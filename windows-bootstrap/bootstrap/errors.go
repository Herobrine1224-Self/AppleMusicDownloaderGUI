package bootstrap

import "fmt"

type ErrorCode string

const (
	CodeUnsupported       ErrorCode = "unsupported_platform"
	CodeIntegrity         ErrorCode = "integrity_check_failed"
	CodePlatform          ErrorCode = "wsl_platform_unavailable"
	CodeRebootRequired    ErrorCode = "reboot_required"
	CodeNameConflict      ErrorCode = "distro_name_conflict"
	CodeOwnership         ErrorCode = "ownership_check_failed"
	CodeRepairRequired    ErrorCode = "repair_required"
	CodeDownload          ErrorCode = "download_failed"
	CodeCommand           ErrorCode = "command_failed"
	CodePortConflict      ErrorCode = "port_conflict"
	CodeRuntimeNotReady   ErrorCode = "runtime_not_ready"
	CodeNotInstalled      ErrorCode = "not_installed"
	CodeConcurrentInstall ErrorCode = "another_install_is_running"
	CodeLoginRequired     ErrorCode = "login_required"
	CodeTwoFactorRequired ErrorCode = "two_factor_required"
	CodeLoginFailed       ErrorCode = "login_failed"
)

type Error struct {
	Code ErrorCode
	Op   string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Op == "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Op, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func Wrap(code ErrorCode, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Op: op, Err: err}
}

func ErrorCodeOf(err error) ErrorCode {
	for err != nil {
		if coded, ok := err.(*Error); ok {
			return coded.Code
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return ""
}
