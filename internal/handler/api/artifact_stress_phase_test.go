//go:build e2e && artifactstress && linux

package api_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
	"github.com/fsnotify/fsnotify"
)

func awaitArtifactPhase(
	t *testing.T,
	watcher *fsnotify.Watcher,
	phase string,
) string {
	t.Helper()
	timeout := time.NewTimer(3 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create == 0 {
				continue
			}
			name := filepath.Base(event.Name)
			if phase == "transport" && name == "stage-ready" {
				return event.Name
			}
			if phase == "extraction" && name == "extraction-ready" {
				return event.Name
			}
			if phase == "download" &&
				strings.HasPrefix(name, "durpdeploy-artifact-") &&
				strings.HasSuffix(name, ".zip") {
				return event.Name
			}
		case err := <-watcher.Errors:
			t.Fatal(err)
		case <-timeout.C:
			t.Fatalf("artifact phase %s was not reached", phase)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func assertInterruptedArtifactPhase(t *testing.T, observed, phase string) {
	t.Helper()
	if phase == "download" {
		info, err := os.Stat(observed)
		if err != nil || info.Size() > 1024 {
			t.Fatalf("download was not held at its partial response: %v", err)
		}
		return
	}
	if phase != "extraction" {
		return
	}
	files := 0
	var bytes int64
	workspace := filepath.Join(
		filepath.Dir(observed),
		"tmp",
		"durpdeploy-artifacts",
	)
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() ||
			!strings.HasPrefix(entry.Name(), "durpdeploy-artifact-") {
			continue
		}
		if err := filepath.WalkDir(
			filepath.Join(workspace, entry.Name()),
			func(_ string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() {
					files++
					info, err := entry.Info()
					if err != nil {
						return err
					}
					bytes += info.Size()
				}
				return err
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if files < 1 || files >= artifact.MaxFiles || bytes < 1 {
		t.Fatal("server was not paused during incomplete extraction")
	}
}
