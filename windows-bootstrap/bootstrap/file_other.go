//go:build !windows

package bootstrap

import (
	"os"
	"path/filepath"
)

func replaceFile(source, destination string) error {
	_ = os.Remove(destination)
	return os.Rename(source, destination)
}

func moveFileNoReplace(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	return os.Remove(source)
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isReparsePoint(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

func canonicalExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
