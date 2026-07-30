package bootstrap

import (
	"context"
	"io"
	"path/filepath"
	"testing"
)

func TestDetachedWSLCommandsUseExecutableDirectory(t *testing.T) {
	wslPath := filepath.Join(t.TempDir(), "System32", "wsl.exe")
	wantDir := filepath.Dir(wslPath)
	state := State{DistroName: "AppleMusic-Runtime-test"}
	tests := []struct {
		name  string
		start func(WSLClient) (Process, error)
	}{
		{
			name: "wrapper",
			start: func(client WSLClient) (Process, error) {
				return client.StartWrapper(state, io.Discard, io.Discard)
			},
		},
		{
			name: "login",
			start: func(client WSLClient) (Process, error) {
				return client.StartLogin(state, io.Discard, io.Discard)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &captureStartRunner{}
			client := WSLClient{Runner: runner, WSLPath: wslPath}
			if _, err := test.start(client); err != nil {
				t.Fatalf("start command: %v", err)
			}
			if got := runner.command.Dir; got != wantDir {
				t.Fatalf("working directory = %q, want %q", got, wantDir)
			}
		})
	}
}

type captureStartRunner struct {
	command Command
}

func (*captureStartRunner) Run(context.Context, Command) (CommandResult, error) {
	return CommandResult{}, nil
}

func (r *captureStartRunner) Start(command Command, _, _ io.Writer) (Process, error) {
	r.command = command
	return capturedProcess{}, nil
}

type capturedProcess struct{}

func (capturedProcess) PID() int       { return 1 }
func (capturedProcess) Release() error { return nil }
