//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func readLoginInput(jsonOutput bool) (string, string, error) {
	reader := bufio.NewReader(os.Stdin)
	username, err := readInputLine(reader, "Apple ID: ", false, jsonOutput)
	if err != nil {
		return "", "", err
	}
	password, err := readInputLine(reader, "Password: ", true, jsonOutput)
	if err != nil {
		return "", "", err
	}
	return username, password, nil
}

func readTwoFactorInput(jsonOutput bool) (string, error) {
	return readInputLine(bufio.NewReader(os.Stdin), "Six-digit code: ", false, jsonOutput)
}

func readInputLine(reader *bufio.Reader, prompt string, hide, quiet bool) (string, error) {
	console, mode := stdinConsoleMode()
	if !quiet && console {
		fmt.Fprint(os.Stderr, prompt)
	}
	if hide && console {
		if err := setStdinConsoleMode(mode &^ 0x4); err != nil {
			return "", err
		}
		defer setStdinConsoleMode(mode)
	}
	line, err := reader.ReadString('\n')
	if hide && console && !quiet {
		fmt.Fprintln(os.Stderr)
	}
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func stdinConsoleMode() (bool, uint32) {
	var mode uint32
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleMode")
	ok, _, _ := proc.Call(os.Stdin.Fd(), uintptr(unsafe.Pointer(&mode)))
	return ok != 0, mode
}

func setStdinConsoleMode(mode uint32) error {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")
	ok, _, callErr := proc.Call(os.Stdin.Fd(), uintptr(mode))
	if ok == 0 {
		return callErr
	}
	return nil
}
