package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxUbuntuBaseSize = 128 << 20

type ArtifactManager struct {
	Config Config
	Client *http.Client
}

func (a ArtifactManager) VerifyPayload() error {
	actual, err := HashPayload(a.Config.PayloadDir)
	if err != nil {
		return Wrap(CodeIntegrity, "hash wrapper payload", err)
	}
	if !strings.EqualFold(actual, a.Config.PayloadHash) {
		return Wrap(CodeIntegrity, "verify wrapper payload", fmt.Errorf("SHA-256 %s does not match expected %s", actual, a.Config.PayloadHash))
	}
	return nil
}

func HashPayload(payloadDir string) (string, error) {
	root, err := filepath.Abs(payloadDir)
	if err != nil {
		return "", err
	}
	required := []string{
		filepath.Join(root, "wrapper"),
		filepath.Join(root, "rootfs", "system", "bin", "main"),
		filepath.Join(root, "rootfs", "system", "bin", "linker64"),
	}
	for _, name := range required {
		info, err := os.Stat(name)
		if err != nil {
			return "", fmt.Errorf("required payload file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("required payload path %s is not a regular file", name)
		}
	}

	var files []string
	for _, entry := range []string{"wrapper", "rootfs"} {
		start := filepath.Join(root, entry)
		err := filepath.WalkDir(start, func(name string, item os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if item.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("payload contains unsupported symbolic link: %s", name)
			}
			if item.Type().IsRegular() {
				relative, err := filepath.Rel(root, name)
				if err != nil {
					return err
				}
				files = append(files, filepath.ToSlash(relative))
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(files)

	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	for _, relative := range files {
		pathBytes := []byte(relative)
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Stat(fullPath)
		if err != nil {
			return "", err
		}
		if err := writePayloadHashHeader(hash, pathBytes, info.Size()); err != nil {
			return "", err
		}
		file, err := os.Open(fullPath)
		if err != nil {
			return "", err
		}
		_, copyErr := io.CopyBuffer(hash, file, buffer)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writePayloadHashHeader(writer io.Writer, pathBytes []byte, size int64) error {
	if len(pathBytes) > int(^uint32(0)) {
		return errors.New("payload path is too long")
	}
	if size < 0 {
		return errors.New("payload file has a negative size")
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(pathBytes))); err != nil {
		return err
	}
	if _, err := writer.Write(pathBytes); err != nil {
		return err
	}
	return binary.Write(writer, binary.BigEndian, uint64(size))
}

func (a ArtifactManager) ResolveUbuntuBase(ctx context.Context) (string, error) {
	if a.Config.UbuntuBasePath != "" {
		if err := verifyFileSHA256(a.Config.UbuntuBasePath, a.Config.UbuntuBaseHash); err != nil {
			return "", Wrap(CodeIntegrity, "verify supplied Ubuntu Base archive", err)
		}
		return a.Config.UbuntuBasePath, nil
	}
	cacheDir := filepath.Join(a.Config.AppDataDir, "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", err
	}
	destination := filepath.Join(cacheDir, "ubuntu-base-24.04.4-amd64.tar.gz")
	if err := verifyFileSHA256(destination, a.Config.UbuntuBaseHash); err == nil {
		return destination, nil
	}
	if err := a.download(ctx, destination); err != nil {
		return "", Wrap(CodeDownload, "download Ubuntu Base", err)
	}
	if err := verifyFileSHA256(destination, a.Config.UbuntuBaseHash); err != nil {
		return "", Wrap(CodeIntegrity, "verify downloaded Ubuntu Base archive", err)
	}
	return destination, nil
}

func (a ArtifactManager) download(ctx context.Context, destination string) error {
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: a.Config.DownloadTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Config.UbuntuBaseURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "AppleMusicDownloader-WSL-Bootstrap/1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", response.Status)
	}
	if response.ContentLength > maxUbuntuBaseSize {
		return fmt.Errorf("archive is unexpectedly large: %d bytes", response.ContentLength)
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".ubuntu-base-*.part")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	limited := io.LimitReader(response.Body, maxUbuntuBaseSize+1)
	written, copyErr := io.Copy(temp, limited)
	if syncErr := temp.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := temp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	if written > maxUbuntuBaseSize {
		return errors.New("download exceeded the maximum allowed archive size")
	}
	if err := verifyFileSHA256(tempName, a.Config.UbuntuBaseHash); err != nil {
		return err
	}
	return replaceFile(tempName, destination)
}

func verifyFileSHA256(name, expected string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("SHA-256 %s does not match expected %s", actual, expected)
	}
	return nil
}

func (a ArtifactManager) BuildRuntimeArchive(baseArchive string, state State) (string, string, error) {
	if !strings.EqualFold(state.PayloadSHA256, a.Config.PayloadHash) || !strings.EqualFold(state.UbuntuBaseSHA256, a.Config.UbuntuBaseHash) {
		return "", "", errors.New("runtime state artifact hashes do not match this bootstrap build")
	}
	runtimeDir := filepath.Join(a.Config.AppDataDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		return "", "", err
	}
	destination := filepath.Join(runtimeDir, state.DistroName+".tar")
	temp, err := os.CreateTemp(runtimeDir, ".runtime-*.tar")
	if err != nil {
		return "", "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	hash := sha256.New()
	tarWriter := tar.NewWriter(io.MultiWriter(temp, hash))
	if err := copyBaseArchive(tarWriter, baseArchive, state.UbuntuBaseSHA256); err != nil {
		tarWriter.Close()
		temp.Close()
		return "", "", err
	}
	payloadHash, err := appendRuntimeFiles(tarWriter, a.Config.PayloadDir, state)
	if err != nil {
		tarWriter.Close()
		temp.Close()
		return "", "", err
	}
	if !strings.EqualFold(payloadHash, state.PayloadSHA256) {
		tarWriter.Close()
		temp.Close()
		return "", "", fmt.Errorf("archived payload SHA-256 %s does not match expected %s", payloadHash, state.PayloadSHA256)
	}
	if err := tarWriter.Close(); err != nil {
		temp.Close()
		return "", "", err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return "", "", err
	}
	if err := temp.Close(); err != nil {
		return "", "", err
	}
	if err := replaceFile(tempName, destination); err != nil {
		return "", "", err
	}
	return destination, hex.EncodeToString(hash.Sum(nil)), nil
}

func copyBaseArchive(writer *tar.Writer, archivePath, expectedHash string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	archiveHash := sha256.New()
	gzipReader, err := gzip.NewReader(io.TeeReader(file, archiveHash))
	if err != nil {
		return fmt.Errorf("open Ubuntu Base gzip stream: %w", err)
	}
	reader := tar.NewReader(gzipReader)
	buffer := make([]byte, 1<<20)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read Ubuntu Base tar: %w", err)
		}
		cleaned, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		if cleaned == "etc/wsl.conf" || cleaned == "etc/applemusic-runtime.json" || cleaned == "opt/applemusic-wrapper" || strings.HasPrefix(cleaned, "opt/applemusic-wrapper/") {
			continue
		}
		copied := *header
		copied.Name = cleaned
		if err := writer.WriteHeader(&copied); err != nil {
			return err
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			if _, err := io.CopyBuffer(writer, reader, buffer); err != nil {
				return err
			}
		}
	}
	if _, err := io.Copy(io.Discard, gzipReader); err != nil {
		gzipReader.Close()
		return fmt.Errorf("finish Ubuntu Base gzip stream: %w", err)
	}
	if err := gzipReader.Close(); err != nil {
		return err
	}
	// gzip may buffer compressed bytes beyond the tar end marker. Those bytes
	// have already passed through the TeeReader; hash any bytes still unread
	// from the file so the comparison covers the complete source archive.
	if _, err := io.Copy(archiveHash, file); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(archiveHash.Sum(nil))
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("Ubuntu Base SHA-256 %s does not match expected %s while archiving", actualHash, expectedHash)
	}
	return nil
}

func safeArchivePath(name string) (string, error) {
	cleaned := path.Clean(strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./"))
	if cleaned == "." || cleaned == "" || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe path in base archive: %q", name)
	}
	return cleaned, nil
}

func appendRuntimeFiles(writer *tar.Writer, payloadDir string, state State) (string, error) {
	now := time.Unix(0, 0).UTC()
	directories := []string{
		"opt/applemusic-wrapper",
		"opt/applemusic-wrapper/rootfs",
	}
	for _, name := range directories {
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0755, ModTime: now}); err != nil {
			return "", err
		}
	}

	marker := RuntimeMarker{
		ProductID:        ProductID,
		InstanceID:       state.InstanceID,
		RuntimeVersion:   state.RuntimeVersion,
		PayloadSHA256:    state.PayloadSHA256,
		UbuntuBaseSHA256: state.UbuntuBaseSHA256,
	}
	markerData, err := json.Marshal(marker)
	if err != nil {
		return "", err
	}
	markerData = append(markerData, '\n')
	wslConfig := []byte("[boot]\nsystemd=false\n\n[automount]\nenabled=false\nmountFsTab=false\n\n[interop]\nenabled=false\nappendWindowsPath=false\n\n[user]\ndefault=applemusic-runtime\n")
	launcher := []byte(`#!/bin/sh
umask 077
cd /opt/applemusic-wrapper || exit 70
exec /usr/bin/flock --exclusive --nonblock --conflict-exit-code 75 /run/applemusic-wrapper.lock ./wrapper "$@"
`)
	if err := writeTarBytes(writer, "etc/applemusic-runtime.json", markerData, 0644, now); err != nil {
		return "", err
	}
	if err := writeTarBytes(writer, "etc/wsl.conf", wslConfig, 0644, now); err != nil {
		return "", err
	}
	if err := writeTarBytes(writer, "opt/applemusic-wrapper/run-wrapper", launcher, 0755, now); err != nil {
		return "", err
	}

	root, err := filepath.Abs(payloadDir)
	if err != nil {
		return "", err
	}
	var entries []string
	for _, item := range []string{"wrapper", "rootfs"} {
		start := filepath.Join(root, item)
		if err := filepath.WalkDir(start, func(name string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			entries = append(entries, filepath.ToSlash(relative))
			return nil
		}); err != nil {
			return "", err
		}
	}
	sort.Strings(entries)
	payloadHash := sha256.New()
	buffer := make([]byte, 1<<20)
	for _, relative := range entries {
		source := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(source)
		if err != nil {
			return "", err
		}
		name := "opt/applemusic-wrapper/" + relative
		if info.IsDir() {
			if name == "opt/applemusic-wrapper/rootfs" {
				continue
			}
			if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0755, ModTime: now}); err != nil {
				return "", err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("unsupported payload file type: %s", relative)
		}
		if err := writePayloadHashHeader(payloadHash, []byte(relative), info.Size()); err != nil {
			return "", fmt.Errorf("hash payload file %s: %w", relative, err)
		}
		mode := int64(0644)
		if relative == "wrapper" || strings.HasPrefix(relative, "rootfs/system/bin/") {
			mode = 0755
		}
		header := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: info.Size(), ModTime: now}
		if err := writer.WriteHeader(header); err != nil {
			return "", err
		}
		file, err := os.Open(source)
		if err != nil {
			return "", err
		}
		_, copyErr := io.CopyBuffer(io.MultiWriter(writer, payloadHash), io.LimitReader(file, info.Size()), buffer)
		if copyErr == nil {
			position, seekErr := file.Seek(0, io.SeekCurrent)
			if seekErr != nil {
				copyErr = seekErr
			} else if position != info.Size() {
				copyErr = fmt.Errorf("payload file %s changed size while being archived", relative)
			} else {
				var extra [1]byte
				if count, readErr := file.Read(extra[:]); count != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
					copyErr = fmt.Errorf("payload file %s changed while being archived", relative)
				}
			}
		}
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(payloadHash.Sum(nil)), nil
}

func writeTarBytes(writer *tar.Writer, name string, data []byte, mode int64, modTime time.Time) error {
	if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(data)), ModTime: modTime}); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}
