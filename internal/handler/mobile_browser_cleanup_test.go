//go:build mobilebrowser

package handler_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func readMobileBrowserProfile(t *testing.T, receiptPath string) string {
	t.Helper()

	contents, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("read browser profile receipt: %v", err)
	}
	profile := strings.TrimSpace(string(contents))
	if profile == "" {
		t.Fatal("browser profile receipt is empty")
	}
	return profile
}

func assertMobileBrowserResourcesGone(
	t *testing.T,
	profile string,
) {
	t.Helper()

	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("owned Chromium profile remains at %q: %v", profile, err)
	}
	processes, err := exec.Command("pgrep", "-af", profile).CombinedOutput()
	if err == nil {
		t.Fatalf("owned Chromium profile process remains: %s", processes)
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf(
			"inspect owned Chromium profile process: %v: %s",
			err,
			processes,
		)
	}
}
