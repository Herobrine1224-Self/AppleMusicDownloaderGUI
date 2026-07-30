package app

import (
	"fmt"
	"os"
	"path/filepath"
)

type Bundle struct {
	Root          string
	BootstrapDir  string
	BootstrapExe  string
	DownloaderDir string
	DownloaderExe string
	ConfigPath    string
	MP4BoxPath    string
}

func DiscoverBundle() (Bundle, error) {
	root := os.Getenv("APPLEMUSIC_BUNDLE_ROOT")
	if root == "" {
		executable, err := os.Executable()
		if err != nil {
			return Bundle{}, err
		}
		root = filepath.Dir(executable)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{
		Root:          abs,
		BootstrapDir:  filepath.Join(abs, "runtime"),
		BootstrapExe:  filepath.Join(abs, "runtime", "AppleMusicWSL.exe"),
		DownloaderDir: filepath.Join(abs, "downloader"),
		DownloaderExe: filepath.Join(abs, "downloader", "AppleMusicDownloaderCLI.exe"),
		ConfigPath:    filepath.Join(abs, "downloader", "config.yaml"),
		MP4BoxPath:    filepath.Join(abs, "tools", "gpac", "mp4box.exe"),
	}
	for label, path := range map[string]string{
		"WSL 引导器": bundle.BootstrapExe,
		"下载核心":    bundle.DownloaderExe,
		"下载配置":    bundle.ConfigPath,
		"MP4Box":  bundle.MP4BoxPath,
	} {
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			return bundle, fmt.Errorf("安装包不完整：缺少%s (%s)", label, path)
		}
	}
	return bundle, nil
}
