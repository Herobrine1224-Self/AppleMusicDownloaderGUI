//go:build !windows

package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
)

func readLoginInput(jsonOutput bool) (string, string, error) {
	reader := bufio.NewReader(os.Stdin)
	username, err := readPlainLine(reader)
	if err != nil {
		return "", "", err
	}
	password, err := readPlainLine(reader)
	return username, password, err
}

func readTwoFactorInput(jsonOutput bool) (string, error) {
	return readPlainLine(bufio.NewReader(os.Stdin))
}

func readPlainLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
