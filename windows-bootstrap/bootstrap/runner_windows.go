//go:build windows

package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const maxCommandOutput = 4 << 20

type OSRunner struct {
	Timeout time.Duration
}

func (r OSRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	return r.run(ctx, command)
}

func (r OSRunner) run(ctx context.Context, command Command) (CommandResult, error) {
	timeout := r.Timeout
	if command.Timeout != 0 {
		timeout = command.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.SysProcAttr = hiddenProcessAttributes()
	if command.Stdin != nil {
		cmd.Stdin = bytes.NewReader(command.Stdin)
	}
	stdout := &limitedBuffer{remaining: maxCommandOutput}
	stderr := &limitedBuffer{remaining: maxCommandOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: 0}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

func (OSRunner) Start(command Command, stdout, stderr io.Writer) (Process, error) {
	cmd := exec.Command(command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.SysProcAttr = hiddenProcessAttributes()
	if command.Stdin != nil {
		cmd.Stdin = bytes.NewReader(command.Stdin)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{process: cmd.Process}, nil
}

func hiddenProcessAttributes() *syscall.SysProcAttr {
	const createNoWindow = 0x08000000
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

type osProcess struct {
	process *os.Process
}

func (p *osProcess) PID() int       { return p.process.Pid }
func (p *osProcess) Release() error { return p.process.Release() }

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if b.remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	write := data
	if len(write) > b.remaining {
		write = write[:b.remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(write)
	b.remaining -= len(write)
	return originalLength, nil
}

func (b *limitedBuffer) Bytes() []byte {
	if !b.truncated {
		return b.buffer.Bytes()
	}
	return []byte(fmt.Sprintf("%s\n[output truncated]", b.buffer.Bytes()))
}
