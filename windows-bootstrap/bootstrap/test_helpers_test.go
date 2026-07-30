package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

func hashFileForTest(t interface{ Fatal(...any) }, name string) string {
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func testConfig(appData, payload, base, payloadHash, baseHash string) Config {
	return Config{
		AppDataDir:      appData,
		PayloadDir:      payload,
		UbuntuBasePath:  base,
		UbuntuBaseURL:   "https://invalid.example/base.tar.gz",
		UbuntuBaseHash:  baseHash,
		PayloadHash:     payloadHash,
		RuntimeVersion:  RuntimeVersion,
		DownloadTimeout: time.Minute,
		CommandTimeout:  time.Minute,
		StartupTimeout:  time.Second,
	}
}

type immediateLock struct{}

func (immediateLock) Lock(context.Context) (func(), error) { return func() {}, nil }
