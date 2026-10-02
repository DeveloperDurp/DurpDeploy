package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceReclaimsStaleFiles(t *testing.T) {
	// Given: interrupted ZIP, extraction, and tar work, plus unrelated files.
	base := t.TempDir()
	directory := filepath.Join(base, "durpdeploy-artifacts")
	if err := createWorkspaceDirectory(directory); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"durpdeploy-artifact-old.zip", "durpdeploy-artifact-old.tar", "keep",
	} {
		if err := os.WriteFile(
			filepath.Join(directory, name),
			[]byte("data"),
			0600,
		); err != nil {
			t.Fatal(err)
		}
	}
	extracted := filepath.Join(directory, "durpdeploy-artifact-old", "nested")
	if err := os.MkdirAll(extracted, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(extracted, "file"),
		[]byte("data"),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "durpdeploy-artifact-unrelated")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}

	// When: startup takes the lease and reclaims stale work.
	workspace, err := OpenWorkspace(base)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()

	// Then: only artifact paths within the dedicated directory are removed.
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != ".lock" ||
		entries[1].Name() != "keep" {
		t.Fatalf("unexpected remaining entries: %v", entries)
	}
	if data, err := os.ReadFile(
		outside,
	); err != nil ||
		string(data) != "outside" {
		t.Fatalf("unrelated TMPDIR file changed: %q, %v", data, err)
	}
}

func TestWorkspaceRejectsActiveLease(t *testing.T) {
	// Given: one live server has files in its leased workspace.
	base := t.TempDir()
	first, err := OpenWorkspace(base)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	active := filepath.Join(first.Directory, "durpdeploy-artifact-active.zip")
	if err := os.WriteFile(active, []byte("active"), 0600); err != nil {
		t.Fatal(err)
	}

	// When: another server attempts startup with the same TMPDIR.
	second, err := OpenWorkspace(base)
	if second != nil {
		defer second.Close()
	}

	// Then: it fails without touching active work.
	if !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("expected busy workspace, got %v", err)
	}
	if data, err := os.ReadFile(
		active,
	); err != nil ||
		string(data) != "active" {
		t.Fatalf("active file changed: %q, %v", data, err)
	}
}

func TestWorkspaceReopensAfterLeaseRelease(t *testing.T) {
	// Given: a stopped server left its lease file behind.
	base := t.TempDir()
	first, err := OpenWorkspace(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// When: the next server starts.
	second, err := OpenWorkspace(base)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	// Then: the stale lease file does not prevent startup.
	if second.Directory != first.Directory {
		t.Fatal("workspace location changed")
	}
}

func TestWorkspaceRejectsSymlink(t *testing.T) {
	for _, target := range []string{"directory", "lock"} {
		t.Run(target, func(t *testing.T) {
			// Given: a workspace directory or lock is a symlink.
			base := t.TempDir()
			outside := t.TempDir()
			link := filepath.Join(base, "durpdeploy-artifacts")
			if target == "lock" {
				if err := createWorkspaceDirectory(link); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(link, ".lock")
				outside = filepath.Join(outside, "file")
				if err := os.WriteFile(outside, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}

			// When: startup opens the workspace.
			workspace, err := OpenWorkspace(base)
			if workspace != nil {
				defer workspace.Close()
			}

			// Then: it rejects the substituted path before cleanup.
			if err == nil {
				t.Fatal("symlink workspace accepted")
			}
		})
	}
}
