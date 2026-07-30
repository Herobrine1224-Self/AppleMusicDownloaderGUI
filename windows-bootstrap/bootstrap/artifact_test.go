package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOfficialUbuntuBaseBuildsRepositoryRuntime(t *testing.T) {
	if os.Getenv("APPLEMUSIC_RUN_NETWORK_TESTS") != "1" {
		t.Skip("set APPLEMUSIC_RUN_NETWORK_TESTS=1 to download the pinned Ubuntu Base archive")
	}
	work := t.TempDir()
	payload := filepath.Join("..", "..", "wrapper-main")
	config := testConfig(work, payload, "", PayloadSHA256, UbuntuBaseSHA256)
	config.UbuntuBasePath = ""
	config.UbuntuBaseURL = UbuntuBaseURL
	config.DownloadTimeout = 10 * time.Minute
	manager := ArtifactManager{Config: config}
	if err := manager.VerifyPayload(); err != nil {
		t.Fatal(err)
	}
	base, err := manager.ResolveUbuntuBase(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := newState(config, time.Unix(123, 0))
	if err != nil {
		t.Fatal(err)
	}
	archive, _, err := manager.BuildRuntimeArchive(base, state)
	if err != nil {
		t.Fatal(err)
	}
	entries := readTarEntries(t, archive)
	for _, name := range []string{"usr/bin/flock", "usr/sbin/useradd", "usr/sbin/chroot", "etc/applemusic-runtime.json", "opt/applemusic-wrapper/wrapper"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("official runtime archive is missing %s", name)
		}
	}
	if _, directShell := entries["bin/sh"]; !directShell {
		if _, mergedBin := entries["bin"]; !mergedBin {
			t.Error("official runtime archive has neither bin/sh nor the usr-merge bin link")
		}
		if _, mergedShell := entries["usr/bin/sh"]; !mergedShell {
			t.Error("official runtime archive is missing usr/bin/sh")
		}
	}
}

func TestBuildRuntimeArchiveAddsPrivateRuntime(t *testing.T) {
	work := t.TempDir()
	payload := makeTestPayload(t, work)
	base := filepath.Join(work, "base.tar.gz")
	writeTestBase(t, base, false)
	payloadHash, err := HashPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	baseHash := hashFileForTest(t, base)
	config := testConfig(work, payload, base, payloadHash, baseHash)
	state, err := newState(config, time.Unix(123, 0))
	if err != nil {
		t.Fatal(err)
	}
	manager := ArtifactManager{Config: config}
	archive, digest, err := manager.BuildRuntimeArchive(base, state)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Fatal("runtime archive digest is empty")
	}

	entries := readTarEntries(t, archive)
	for _, name := range []string{
		"bin/sh",
		"etc/wsl.conf",
		"etc/applemusic-runtime.json",
		"opt/applemusic-wrapper/wrapper",
		"opt/applemusic-wrapper/run-wrapper",
		"opt/applemusic-wrapper/rootfs/system/bin/main",
	} {
		if _, ok := entries[name]; !ok {
			t.Errorf("runtime archive is missing %s", name)
		}
	}
	if !strings.Contains(string(entries["etc/wsl.conf"].data), "default=applemusic-runtime") {
		t.Error("wsl.conf does not select the restricted runtime user")
	}
	if !strings.Contains(string(entries["etc/wsl.conf"].data), "enabled=false") {
		t.Error("wsl.conf does not disable private-runtime integration")
	}
	if entries["opt/applemusic-wrapper/wrapper"].mode != 0755 || entries["opt/applemusic-wrapper/run-wrapper"].mode != 0755 {
		t.Error("runtime executables are not marked executable")
	}
	launcher := string(entries["opt/applemusic-wrapper/run-wrapper"].data)
	if !strings.Contains(launcher, "/usr/bin/flock") || strings.Contains(launcher, "rm -rf") {
		t.Fatalf("runtime launcher does not use a kernel-held lock: %q", launcher)
	}
	var marker RuntimeMarker
	if err := json.Unmarshal(entries["etc/applemusic-runtime.json"].data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.ProductID != ProductID || marker.InstanceID != state.InstanceID || marker.RuntimeVersion != state.RuntimeVersion || marker.PayloadSHA256 != payloadHash || marker.UbuntuBaseSHA256 != state.UbuntuBaseSHA256 {
		t.Fatalf("unexpected runtime marker: %#v", marker)
	}
	extractedPayload := filepath.Join(work, "extracted-payload")
	for name, entry := range entries {
		const prefix = "opt/applemusic-wrapper/"
		if !strings.HasPrefix(name, prefix) || name == prefix+"run-wrapper" || entry.typeflag != tar.TypeReg && entry.typeflag != tar.TypeRegA {
			continue
		}
		relative := strings.TrimPrefix(name, prefix)
		if relative == "" || relative == "run-wrapper" {
			continue
		}
		destination := filepath.Join(extractedPayload, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, entry.data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	extractedHash, err := HashPayload(extractedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if extractedHash != marker.PayloadSHA256 {
		t.Fatalf("archived payload SHA-256 = %s, marker = %s", extractedHash, marker.PayloadSHA256)
	}
}

func TestBuildRuntimeArchiveRejectsPayloadChangedAfterStateWasCreated(t *testing.T) {
	work := t.TempDir()
	payload := makeTestPayload(t, work)
	base := filepath.Join(work, "base.tar.gz")
	writeTestBase(t, base, false)
	config := testConfig(work, payload, base, hashPayloadForTest(t, payload), hashFileForTest(t, base))
	state, err := newState(config, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "rootfs", "system", "lib64", "libc.so"), []byte("changed-library"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err = (ArtifactManager{Config: config}).BuildRuntimeArchive(base, state)
	if err == nil || !strings.Contains(err.Error(), "archived payload SHA-256") {
		t.Fatalf("BuildRuntimeArchive() error = %v, want archived payload hash mismatch", err)
	}
}

func TestBuildRuntimeArchiveRejectsBaseChangedAfterStateWasCreated(t *testing.T) {
	work := t.TempDir()
	payload := makeTestPayload(t, work)
	base := filepath.Join(work, "base.tar.gz")
	writeTestBase(t, base, false)
	config := testConfig(work, payload, base, hashPayloadForTest(t, payload), hashFileForTest(t, base))
	state, err := newState(config, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	writeTestBaseWithExtra(t, base)
	_, _, err = (ArtifactManager{Config: config}).BuildRuntimeArchive(base, state)
	if err == nil || !strings.Contains(err.Error(), "Ubuntu Base SHA-256") {
		t.Fatalf("BuildRuntimeArchive() error = %v, want Ubuntu Base hash mismatch", err)
	}
}

func TestHashPayloadFramesAdjacentFiles(t *testing.T) {
	work := t.TempDir()
	payload := makeTestPayload(t, work)
	aPath := filepath.Join(payload, "rootfs", "system", "lib64", "a.so")
	bPath := filepath.Join(payload, "rootfs", "system", "lib64", "b.so")
	if err := os.WriteFile(aPath, []byte("left"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("right"), 0644); err != nil {
		t.Fatal(err)
	}
	first := hashPayloadForTest(t, payload)
	if err := os.Remove(bPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aPath, []byte("leftrootfs/system/lib64/b.so\x00right"), 0644); err != nil {
		t.Fatal(err)
	}
	second := hashPayloadForTest(t, payload)
	if first == second {
		t.Fatal("payload hash did not frame adjacent file records")
	}
}

func TestBuildRuntimeArchiveRejectsTraversal(t *testing.T) {
	work := t.TempDir()
	payload := makeTestPayload(t, work)
	base := filepath.Join(work, "unsafe.tar.gz")
	writeTestBase(t, base, true)
	payloadHash, _ := HashPayload(payload)
	baseHash := hashFileForTest(t, base)
	config := testConfig(work, payload, base, payloadHash, baseHash)
	state, _ := newState(config, time.Now())
	_, _, err := (ArtifactManager{Config: config}).BuildRuntimeArchive(base, state)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("BuildRuntimeArchive() error = %v, want unsafe path error", err)
	}
}

func TestRepositoryPayloadMatchesPinnedHash(t *testing.T) {
	payload := filepath.Join("..", "..", "wrapper-main")
	if _, err := os.Stat(filepath.Join(payload, "wrapper")); err != nil {
		t.Skip("repository wrapper payload is not available")
	}
	actual, err := HashPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if actual != PayloadSHA256 {
		t.Fatalf("repository payload SHA-256 = %s, want %s; refuse to package modified or stateful payload", actual, PayloadSHA256)
	}
}

func makeTestPayload(t *testing.T, root string) string {
	t.Helper()
	payload := filepath.Join(root, "payload")
	files := map[string]string{
		"wrapper":                          "wrapper-binary",
		"rootfs/system/bin/main":           "android-main",
		"rootfs/system/bin/linker64":       "android-linker",
		"rootfs/system/lib64/libc.so":      "android-libc",
		"rootfs/data/data/app/files/.keep": "",
	}
	for name, contents := range files {
		full := filepath.Join(payload, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return payload
}

func hashPayloadForTest(t *testing.T, payload string) string {
	t.Helper()
	hash, err := HashPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func writeTestBase(t *testing.T, destination string, traversal bool) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	entries := map[string]string{
		"bin/sh":       "shell",
		"etc/passwd":   "root:x:0:0:root:/root:/bin/sh\n",
		"etc/wsl.conf": "[interop]\nenabled=true\n",
	}
	if traversal {
		entries["../../outside"] = "bad"
	}
	for name, contents := range entries {
		header := &tar.Header{Name: name, Mode: 0644, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestBaseWithExtra(t *testing.T, destination string) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	contents := "different-base"
	if err := tw.WriteHeader(&tar.Header{Name: "bin/sh", Mode: 0755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, contents); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

type tarEntry struct {
	data     []byte
	mode     int64
	typeflag byte
}

func readTarEntries(t *testing.T, archive string) map[string]tarEntry {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	entries := make(map[string]tarEntry)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		entries[header.Name] = tarEntry{data: data, mode: header.Mode, typeflag: header.Typeflag}
	}
	return entries
}
