package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"applemusic/wslbootstrap/bootstrap"
)

const (
	exitFailure          = 1
	exitUsage            = 2
	exitRebootRequired   = bootstrap.ExitRebootRequired
	exitUnsupported      = 21
	exitIntegrityFailure = 22
	exitOwnershipFailure = 23
	exitRepairRequired   = 24
	exitRuntimeNotReady  = 25
	exitLoginRequired    = 26
	exitTwoFactor        = 27
	exitLoginFailed      = 28
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return exitUsage
	}

	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	payload := flags.String("payload", "", "directory containing wrapper and rootfs")
	ubuntuBase := flags.String("ubuntu-base", "", "optional local Ubuntu WSL image (.wsl)")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	backup := flags.String("backup", "", "backup tar path used by remove")
	if err := flags.Parse(args[1:]); err != nil {
		return exitUsage
	}

	var config bootstrap.Config
	var err error
	if command == "install" || command == "verify" {
		config, err = bootstrap.DefaultConfig(*payload, *ubuntuBase)
	} else {
		config, err = bootstrap.DefaultManagementConfig()
	}
	if err != nil {
		printError(err, *jsonOutput)
		return exitForError(err)
	}
	manager, err := bootstrap.NewManager(config)
	if err != nil {
		printError(err, *jsonOutput)
		return exitForError(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch command {
	case "install":
		status, err := manager.Install(ctx)
		return printStatusResult(status, err, *jsonOutput)
	case "status":
		status, err := manager.Status(ctx)
		return printStatusResult(status, err, *jsonOutput)
	case "verify":
		err := manager.Artifacts.VerifyPayload()
		if err != nil {
			printError(err, *jsonOutput)
			return exitForError(err)
		}
		if *jsonOutput {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"verified": true, "payload_sha256": config.PayloadHash})
		} else {
			fmt.Printf("Payload 校验通过：%s\n", config.PayloadHash)
		}
		return 0
	case "start":
		status, err := manager.Start(ctx)
		return printStatusResult(status, err, *jsonOutput)
	case "login":
		username, password, inputErr := readLoginInput(*jsonOutput)
		if inputErr != nil {
			inputErr = bootstrap.Wrap(bootstrap.CodeLoginFailed, "read login credentials", inputErr)
			printError(inputErr, *jsonOutput)
			return exitForError(inputErr)
		}
		status, err := manager.Login(ctx, username, password)
		return printStatusResult(status, err, *jsonOutput)
	case "submit-code":
		code, inputErr := readTwoFactorInput(*jsonOutput)
		if inputErr != nil {
			inputErr = bootstrap.Wrap(bootstrap.CodeLoginFailed, "read two-factor code", inputErr)
			printError(inputErr, *jsonOutput)
			return exitForError(inputErr)
		}
		status, err := manager.SubmitTwoFactorCode(ctx, code)
		return printStatusResult(status, err, *jsonOutput)
	case "logout":
		status, err := manager.Logout(ctx)
		return printStatusResult(status, err, *jsonOutput)
	case "stop":
		err := manager.Stop(ctx)
		if err == nil {
			if *jsonOutput {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"stopped": true})
			} else {
				fmt.Println("AppleMusic 专用 WSL 已停止。")
			}
			return 0
		}
		printError(err, *jsonOutput)
		return exitForError(err)
	case "remove":
		path, err := manager.RemoveWithBackup(ctx, *backup)
		if err != nil {
			printError(err, *jsonOutput)
			return exitForError(err)
		}
		if *jsonOutput {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"removed": true, "backup_path": path})
		} else {
			fmt.Printf("专用 WSL 已备份并删除。备份位置：%s\n", path)
		}
		return 0
	default:
		printUsage()
		return exitUsage
	}
}

func printStatusResult(status bootstrap.Status, err error, jsonOutput bool) int {
	if jsonOutput {
		result := struct {
			Status bootstrap.Status `json:"status"`
			Error  any              `json:"error,omitempty"`
		}{Status: status}
		if err != nil {
			result.Error = map[string]string{"code": string(bootstrap.ErrorCodeOf(err)), "message": err.Error()}
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
	} else if err == nil {
		fmt.Printf("发行版：%s\n阶段：%s\n已安装：%t\n运行中：%t\n健康：%t\n", status.DistroName, status.Stage, status.Installed, status.Running, status.Healthy)
	} else {
		printError(err, false)
	}
	return exitForError(err)
}

func printError(err error, jsonOutput bool) {
	if jsonOutput {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"error": map[string]string{"code": string(bootstrap.ErrorCodeOf(err)), "message": err.Error()},
		})
		return
	}
	fmt.Fprintf(os.Stderr, "错误：%v\n", err)
}

func exitForError(err error) int {
	if err == nil {
		return 0
	}
	switch bootstrap.ErrorCodeOf(err) {
	case bootstrap.CodeRebootRequired:
		return exitRebootRequired
	case bootstrap.CodeUnsupported:
		return exitUnsupported
	case bootstrap.CodeIntegrity:
		return exitIntegrityFailure
	case bootstrap.CodeOwnership, bootstrap.CodeNameConflict:
		return exitOwnershipFailure
	case bootstrap.CodeRepairRequired:
		return exitRepairRequired
	case bootstrap.CodeRuntimeNotReady, bootstrap.CodePortConflict:
		return exitRuntimeNotReady
	case bootstrap.CodeLoginRequired:
		return exitLoginRequired
	case bootstrap.CodeTwoFactorRequired:
		return exitTwoFactor
	case bootstrap.CodeLoginFailed:
		return exitLoginFailed
	default:
		return exitFailure
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `AppleMusic 专用 WSL 管理器

用法:
  applemusic-wsl install [--payload DIR] [--ubuntu-base FILE] [--json]
  applemusic-wsl verify [--payload DIR] [--json]
  applemusic-wsl status [--json]
  applemusic-wsl start [--json]
  applemusic-wsl login [--json]
  applemusic-wsl submit-code [--json]
  applemusic-wsl logout [--json]
  applemusic-wsl stop
  applemusic-wsl remove [--backup FILE] [--json]

remove 总是先导出完整备份，再注销本程序拥有的发行版。`)
}
