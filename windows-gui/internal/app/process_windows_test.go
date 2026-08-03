//go:build windows

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCommandInKillOnCloseJobAllowsFastExit(t *testing.T) {
	commandInterpreter := os.Getenv("COMSPEC")
	if commandInterpreter == "" {
		commandInterpreter = "cmd.exe"
	}
	for i := 0; i < 20; i++ {
		command := hiddenCommand(context.Background(), commandInterpreter, "/d", "/c", "exit 0")
		if err := runCommandInKillOnCloseJob(command); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

func TestRunCommandInKillOnCloseJobKillsDescendants(t *testing.T) {
	root := t.TempDir()
	readyPath := filepath.Join(root, "ready")
	leakedPath := filepath.Join(root, "leaked")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	command := hiddenCommand(ctx, os.Args[0], "-test.run=^TestCleanupJobHelperProcess$", "--", "parent", readyPath, leakedPath)
	ready := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(readyPath); err == nil {
				cancel()
				ready <- nil
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		ready <- os.ErrDeadlineExceeded
	}()

	if err := runCommandInKillOnCloseJob(command); err == nil {
		t.Fatal("canceled helper process exited successfully")
	}
	if err := <-ready; err != nil {
		t.Fatal("helper process did not start its child before cancellation")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(leakedPath); !os.IsNotExist(err) {
		t.Fatalf("descendant survived cleanup job: %v", err)
	}
}

func TestCleanupJobHelperProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	args := os.Args[separator+1:]
	switch args[0] {
	case "parent":
		if len(args) != 3 {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestCleanupJobHelperProcess$", "--", "child", args[2])
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		_ = child.Process.Release()
		if err := os.WriteFile(args[1], []byte("ready"), 0600); err != nil {
			os.Exit(4)
		}
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "child":
		if len(args) != 2 {
			os.Exit(5)
		}
		time.Sleep(time.Second)
		if err := os.WriteFile(args[1], []byte("survived"), 0600); err != nil {
			os.Exit(6)
		}
		os.Exit(0)
	}
}
