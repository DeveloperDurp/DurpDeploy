//go:build e2e && artifactstress && linux

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
)

func TestArtifactCrashRecoveryE2E(t *testing.T) {
	binary := buildArtifactServer(t)
	archive := nearLimitArtifact(t)
	for _, phase := range []string{"download", "extraction", "transport"} {
		t.Run(phase, func(t *testing.T) {
			// Given: a real server process, immutable pin, and unrelated resources.
			p := newArtifactProcessFixture(t, binary)
			p.phase = phase
			f := p.api
			f.changePackage("file:" + archive)
			_, release := f.createArtifactRelease(
				t,
				"set -eu; test -d \"$ARTIFACT_PATH\"",
			)
			pinPath := fmt.Sprintf(
				"%s/releases/%d/artifact",
				f.base(),
				release.ID,
			)
			before := f.api(t, "GET", pinPath, nil, 200)
			f.server.Close()
			container, volume := p.unrelatedResources(t)
			keep := filepath.Join(p.workspace(), "keep.txt")
			outside := filepath.Join(
				p.root,
				"tmp",
				"durpdeploy-artifact-unrelated.zip",
			)
			for _, path := range []string{keep, outside} {
				if err := os.WriteFile(
					path,
					[]byte("unrelated"),
					0600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "transport" {
				p.gateTransport(t)
			}
			if phase == "extraction" {
				if err := unix.Mkfifo(
					filepath.Join(p.root, "extraction-release"),
					0600,
				); err != nil {
					t.Fatal(err)
				}
			}
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				t.Fatal(err)
			}
			defer watcher.Close()
			for _, directory := range []string{p.workspace(), p.root} {
				if err := watcher.Add(directory); err != nil {
					t.Fatal(err)
				}
			}
			process := p.start(t)
			if phase == "download" {
				f.changePackage("blocked:" + archive)
			}
			body := f.api(t, "POST", f.base()+"/deployments", map[string]int64{
				"release_id": release.ID, "environment_id": f.environment.ID,
			}, 201)
			var deployment db.Deployment
			if err := json.Unmarshal(body, &deployment); err != nil {
				t.Fatal(err)
			}
			observed := awaitArtifactPhase(t, watcher, phase)
			if phase == "transport" {
				keepers := strings.Fields(p.ownedContainers(t))
				if len(keepers) != 1 {
					t.Fatalf("expected one staging keeper, got %v", keepers)
				}
				p.runtime(
					t,
					"exec",
					keepers[0],
					"sh",
					"-c",
					"while ! test -s /artifacts/d0000/package.txt; do :; done; files=$(find /artifacts -type f | wc -l); test \"$files\" -gt 0; test \"$files\" -lt 10000",
				)
			}
			process.pause(t)
			assertInterruptedArtifactPhase(t, observed, phase)
			if phase == "transport" &&
				(p.ownedContainers(t) == "" || p.ownedVolumes(t) == "") {
				t.Fatal(
					"transport crash did not leave real staging runtime resources",
				)
			}

			// When: SIGKILL prevents defers from running, then the server restarts.
			process.kill(t)
			f.changePackage("file:" + archive)
			restarted := p.start(t)

			// Then: startup reclaims only owned files and runtime resources.
			entries, err := os.ReadDir(p.workspace())
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "durpdeploy-artifact-") {
					t.Fatalf(
						"stale artifact remains after restart: %s",
						entry.Name(),
					)
				}
			}
			if p.ownedContainers(t) != "" || p.ownedVolumes(t) != "" {
				t.Fatal("stale runtime staging resources remain after restart")
			}
			for _, path := range []string{keep, outside} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "unrelated" {
					t.Fatalf("unrelated file changed: %s (%v)", path, err)
				}
			}
			p.runtime(t, "inspect", container)
			p.runtime(t, "volume", "inspect", volume)
			status := f.api(
				t,
				"GET",
				fmt.Sprintf("/api/v1/deployments/%d/status", deployment.ID),
				nil,
				200,
			)
			var state struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(
				status,
				&state,
			); err != nil ||
				state.Status != "failed" {
				t.Fatalf(
					"orphaned deployment was not finalized: %s (%v)",
					status,
					err,
				)
			}
			after := f.api(t, "GET", pinPath, nil, 200)
			if !bytes.Equal(before, after) {
				t.Fatal("restart changed the immutable pin")
			}
			restarted.kill(t)
		})
	}
}
