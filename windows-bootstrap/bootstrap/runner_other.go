//go:build !windows

package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

type OSRunner struct{ Timeout time.Duration }

func (OSRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdin = bytes.NewReader(command.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

func (OSRunner) Start(command Command, stdout, stderr io.Writer) (Process, error) {
	return nil, errors.New("detached runtime launch is only supported on Windows")
}
