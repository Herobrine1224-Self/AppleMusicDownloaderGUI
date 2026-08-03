//go:build windows

package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

func hiddenCommand(ctx context.Context, executable string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, executable, args...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return command
}

type BootstrapClient struct {
	Bundle Bundle
}

func (c BootstrapClient) Invoke(ctx context.Context, operation string, stdin []byte, extraArgs ...string) (BootstrapResponse, error) {
	args := []string{operation, "--json"}
	args = append(args, extraArgs...)
	command := hiddenCommand(ctx, c.Bundle.BootstrapExe, args...)
	command.Dir = c.Bundle.BootstrapDir
	command.Env = append(os.Environ(), "APPLEMUSIC_BUNDLE_ROOT="+c.Bundle.Root)
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	var runErr error
	if operation == "stop" {
		runErr = runCommandInKillOnCloseJob(command)
	} else {
		runErr = command.Run()
	}
	response, decodeErr := DecodeBootstrapResponse(stdout.Bytes())
	if decodeErr != nil {
		if ctx.Err() != nil {
			return BootstrapResponse{}, ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = decodeErr.Error()
		}
		return BootstrapResponse{}, &OperationError{Code: "invalid_response", Message: message, ExitCode: exitCode(runErr)}
	}
	if response.Error != nil {
		return response, &OperationError{Code: response.Error.Code, Message: response.Error.Message, ExitCode: exitCode(runErr)}
	}
	if runErr != nil {
		return response, &OperationError{Code: "process_failed", Message: strings.TrimSpace(stderr.String()), ExitCode: exitCode(runErr)}
	}
	return response, nil
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

func runCommandInKillOnCloseJob(command *exec.Cmd) error {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := command.Start(); err != nil {
		return err
	}
	closeJob, jobErr := assignKillOnCloseJob(command.Process.Pid)
	if jobErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("assign bootstrap stop to cleanup job: %w", jobErr)
	}
	if err := resumeProcess(command.Process.Pid); err != nil {
		closeJob()
		_ = command.Wait()
		return fmt.Errorf("resume bootstrap stop in cleanup job: %w", err)
	}
	runErr := command.Wait()
	closeJob()
	return runErr
}

func resumeProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == uint32(pid) {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			return resumeErr
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return fmt.Errorf("no thread found for process %d", pid)
			}
			return err
		}
	}
}

type DownloadOptions struct {
	Link           LinkInfo
	OutputDir      string
	Quality        string
	SongFileFormat string
	SelectIndexes  string
	TaskID         string
	OnEvent        func(DownloadEvent)
	OnLog          func(string)
}

type TrackItem struct {
	Index       int    `json:"index"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	TrackNumber int    `json:"track_number"`
	DiscNumber  int    `json:"disc_number"`
	DurationMs  int    `json:"duration_ms"`
	Type        string `json:"type"`
}

type TrackGroup struct {
	URL    string      `json:"url"`
	Title  string      `json:"title"`
	Artist string      `json:"artist"`
	Tracks []TrackItem `json:"tracks"`
}

func ListTracks(ctx context.Context, bundle Bundle, options DownloadOptions) ([]TrackGroup, error) {
	args := []string{
		"--gui",
		"--non-interactive",
		"--config", bundle.ConfigPath,
		"--mp4box", bundle.MP4BoxPath,
		"--list-tracks",
	}
	if options.Link.Kind == "artist" {
		args = append(args, "--all-album")
	}
	args = append(args, options.Link.URL)

	command := hiddenCommand(ctx, bundle.DownloaderExe, args...)
	command.Dir = bundle.DownloaderDir
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()

	var lines []string
	scanLines(&stdout, func(line string) { lines = append(lines, line) })
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.TrimPrefix(lines[i], "\ufeff"))
		if !strings.HasPrefix(line, "[") {
			continue
		}
		var groups []TrackGroup
		if err := json.Unmarshal([]byte(line), &groups); err == nil {
			return groups, nil
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	message := strings.TrimSpace(stderr.String())
	if message == "" && len(lines) > 0 {
		message = strings.TrimSpace(lines[len(lines)-1])
	}
	if message == "" {
		message = "无法获取歌曲列表"
	}
	return nil, &OperationError{Code: "list_tracks_failed", Message: message, ExitCode: exitCode(runErr)}
}

func RunDownload(ctx context.Context, bundle Bundle, options DownloadOptions) error {
	args := []string{
		"--gui",
		"--non-interactive",
		"--config", bundle.ConfigPath,
		"--output", options.OutputDir,
		"--mp4box", bundle.MP4BoxPath,
		"--task-id", options.TaskID,
	}
	if options.Quality == QualityAtmos {
		args = append(args, "--atmos")
	}
	if options.SongFileFormat != "" {
		args = append(args, "--song-file-format", options.SongFileFormat)
	}
	if options.SelectIndexes != "" {
		args = append(args, "--select-indexes", options.SelectIndexes)
	}
	if options.Link.SingleSong {
		args = append(args, "--song")
	}
	if options.Link.Kind == "artist" {
		args = append(args, "--all-album")
	}
	args = append(args, options.Link.URL)

	command := hiddenCommand(ctx, bundle.DownloaderExe, args...)
	command.Dir = bundle.DownloaderDir
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	closeJob, _ := assignKillOnCloseJob(command.Process.Pid)
	jobDone := make(chan struct{})
	if closeJob != nil {
		go func() {
			select {
			case <-ctx.Done():
				closeJob()
			case <-jobDone:
			}
		}()
	}

	var wait sync.WaitGroup
	var lastEventError string
	var eventMu sync.Mutex
	wait.Add(2)
	go func() {
		defer wait.Done()
		scanLines(stdout, func(line string) {
			if event, ok := DecodeDownloadEvent(line); ok {
				if event.Event == "error" {
					eventMu.Lock()
					lastEventError = event.Message
					if event.Detail != "" {
						lastEventError += "：" + event.Detail
					}
					eventMu.Unlock()
				}
				if options.OnEvent != nil {
					options.OnEvent(event)
				}
				return
			}
			if options.OnLog != nil && strings.TrimSpace(line) != "" {
				options.OnLog(line)
			}
		})
	}()
	go func() {
		defer wait.Done()
		scanLines(stderr, func(line string) {
			if options.OnLog != nil && strings.TrimSpace(line) != "" {
				options.OnLog(line)
			}
		})
	}()
	wait.Wait()
	runErr := command.Wait()
	close(jobDone)
	if closeJob != nil {
		closeJob()
	}
	cleanupTaskPartials(options.OutputDir, options.TaskID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if runErr != nil {
		eventMu.Lock()
		message := lastEventError
		eventMu.Unlock()
		if message == "" {
			message = fmt.Sprintf("下载核心退出，代码 %d", exitCode(runErr))
		}
		return &OperationError{Code: "download_failed", Message: message, ExitCode: exitCode(runErr)}
	}
	return nil
}

func assignKillOnCloseJob(pid int) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = windows.CloseHandle(job) }
	var information windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		cleanup()
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		cleanup()
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		cleanup()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(cleanup) }, nil
}

func cleanupTaskPartials(root, taskID string) {
	if taskID == "" {
		return
	}
	suffix := ".partial-" + taskID + ".m4a"
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), suffix) {
			_ = os.Remove(path)
		}
		return nil
	})
}

func scanLines(reader io.Reader, callback func(string)) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		callback(scanner.Text())
	}
}
