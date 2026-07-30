//go:build windows

package bootstrap

import (
	"context"
	"strings"
	"testing"
)

func TestEnableWSLFeaturesUsesNoDistributionInstallAndLegacyUpdateFallback(t *testing.T) {
	runner := &recordingRunner{responses: []CommandResult{
		{ExitCode: 1, Stderr: []byte("unsupported option")},
		{ExitCode: 0},
		{ExitCode: 0},
		{ExitCode: 1, Stderr: []byte("unsupported option")},
		{ExitCode: 0},
	}}
	reboot, err := enableWSLFeaturesAt(context.Background(), runner, `C:\Windows`)
	if err != nil {
		t.Fatal(err)
	}
	if reboot {
		t.Fatal("enableWSLFeaturesAt() requested an unexpected reboot")
	}
	if len(runner.calls) != 5 {
		t.Fatalf("got %d commands, want 5", len(runner.calls))
	}
	first := runner.calls[0].Args
	want := []string{"--install", "--no-distribution", "--web-download"}
	if strings.Join(first, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("platform install args = %#v, want %#v", first, want)
	}
	for _, call := range runner.calls {
		if targetsDistro(call.Args, "Ubuntu") || containsArg(call.Args, "Ubuntu") {
			t.Fatalf("platform setup attempted to target Ubuntu: %#v", call.Args)
		}
	}
}

func TestEnableWSLFeaturesPropagatesInstallerRebootCode(t *testing.T) {
	runner := &recordingRunner{responses: []CommandResult{
		{ExitCode: 3010},
		{ExitCode: 0},
		{ExitCode: 0},
	}}
	reboot, err := enableWSLFeaturesAt(context.Background(), runner, `C:\Windows`)
	if err != nil {
		t.Fatal(err)
	}
	if !reboot {
		t.Fatal("enableWSLFeaturesAt() did not propagate exit code 3010")
	}
	if len(runner.calls) != 3 {
		t.Fatalf("got %d commands; update should not run before a required reboot", len(runner.calls))
	}
}
