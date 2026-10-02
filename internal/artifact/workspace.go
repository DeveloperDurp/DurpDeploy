package artifact

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrWorkspaceBusy = errors.New(
	"artifact workspace is already in use; use a separate TMPDIR for each server",
)

type Workspace struct {
	Directory string
	lock      *os.File
}

// OpenWorkspace holds an exclusive lease before reclaiming any stale artifact
// files. Only the dedicated private directory is swept, never the whole TMPDIR.
func OpenWorkspace(base string) (*Workspace, error) {
	directory := filepath.Join(base, "durpdeploy-artifacts")
	if err := createWorkspaceDirectory(directory); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("artifact workspace must be a private directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	openedInfo, statErr := opened.Stat()
	if statErr == nil && !os.SameFile(info, openedInfo) {
		statErr = errors.New(
			"artifact workspace directory changed during startup",
		)
	}
	err = errors.Join(
		statErr,
		validateWorkspaceDirectory(opened),
		opened.Close(),
	)
	if err != nil {
		return nil, err
	}
	if info, err := root.Lstat(".lock"); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New(
				"artifact workspace lock must be a regular file",
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lock, err := root.OpenFile(".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockWorkspace(lock); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	workspace := &Workspace{Directory: directory, lock: lock}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, errors.Join(err, workspace.Close())
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "durpdeploy-artifact-") {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return nil, errors.Join(
				fmt.Errorf("reclaim artifact workspace: %w", err),
				workspace.Close(),
			)
		}
	}
	return workspace, nil
}

func (w *Workspace) Close() error {
	return w.lock.Close()
}
