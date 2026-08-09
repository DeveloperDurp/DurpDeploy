//go:build mobilebrowser

package handler_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMobileBrowserReceiptCreatesEvidenceDirectoryWhenMissing(t *testing.T) {
	// Given
	root := t.TempDir()
	evidenceDir := filepath.Join(root, ".omo", "evidence")
	if _, err := os.Stat(evidenceDir); !os.IsNotExist(err) {
		t.Fatalf("evidence directory exists before receipt write: %v", err)
	}

	// When
	err := writeMobileBrowserReceipt(root, "receipt.json", []byte(`{}`))

	// Then
	if err != nil {
		t.Fatalf("write receipt with missing evidence directory: %v", err)
	}
	evidenceInfo, err := os.Stat(evidenceDir)
	if err != nil {
		t.Fatalf("stat evidence directory: %v", err)
	}
	if got := evidenceInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("evidence directory mode = %o, want %o", got, 0o700)
	}
	receiptInfo, err := os.Stat(filepath.Join(evidenceDir, "receipt.json"))
	if err != nil {
		t.Fatalf("stat receipt: %v", err)
	}
	if got, want := receiptInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("receipt mode = %o, want %o", got, want)
	}
}

func TestMobileBrowserReceiptRejectsTraversalFilename(t *testing.T) {
	// Given
	root := t.TempDir()

	// When
	err := writeMobileBrowserReceipt(root, "../receipt.json", []byte(`{}`))

	// Then
	if err == nil {
		t.Fatal("receipt traversal filename was accepted")
	}
	receiptPath := filepath.Join(root, ".omo", "receipt.json")
	if _, statErr := os.Stat(receiptPath); !os.IsNotExist(statErr) {
		t.Fatalf("receipt traversal created an outside file: %v", statErr)
	}
}

func writeMobileBrowserReceipt(
	root, evidenceFile string,
	receipt []byte,
) error {
	invalidName := evidenceFile == "" ||
		evidenceFile == "." ||
		evidenceFile == ".." ||
		filepath.Base(evidenceFile) != evidenceFile ||
		filepath.IsAbs(evidenceFile)
	if invalidName {
		return fmt.Errorf("invalid evidence filename %q", evidenceFile)
	}
	evidenceDir := filepath.Join(root, ".omo", "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		return err
	}
	temporaryReceipt, err := os.CreateTemp(evidenceDir, ".receipt-")
	if err != nil {
		return err
	}
	temporaryPath := temporaryReceipt.Name()
	defer os.Remove(temporaryPath)
	_, writeErr := temporaryReceipt.Write(append(receipt, '\n'))
	closeErr := temporaryReceipt.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(
		temporaryPath,
		filepath.Join(evidenceDir, evidenceFile),
	)
}

func TestMobileBrowserReceiptReplacesExistingLinks(t *testing.T) {
	for _, test := range []struct {
		name    string
		newLink func(string, string) error
	}{
		{name: "symlink", newLink: os.Symlink},
		{name: "hardlink", newLink: os.Link},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			root := t.TempDir()
			evidenceDir := filepath.Join(root, ".omo", "evidence")
			if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
				t.Fatalf("create evidence directory: %v", err)
			}
			externalPath := filepath.Join(root, "external.json")
			if err := os.WriteFile(
				externalPath,
				[]byte("external"),
				0o600,
			); err != nil {
				t.Fatalf("write external file: %v", err)
			}
			receiptPath := filepath.Join(evidenceDir, "receipt.json")
			if err := test.newLink(externalPath, receiptPath); err != nil {
				t.Fatalf("create %s: %v", test.name, err)
			}

			// When
			err := writeMobileBrowserReceipt(root, "receipt.json", []byte(`{}`))

			// Then
			if err != nil {
				t.Fatalf("write replacement receipt: %v", err)
			}
			external, err := os.ReadFile(externalPath)
			if err != nil {
				t.Fatalf("read external file: %v", err)
			}
			if got, want := string(external), "external"; got != want {
				t.Fatalf("external file = %q, want %q", got, want)
			}
			receipt, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatalf("read replacement receipt: %v", err)
			}
			if got, want := string(receipt), "{}\n"; got != want {
				t.Fatalf("receipt = %q, want %q", got, want)
			}
		})
	}
}
