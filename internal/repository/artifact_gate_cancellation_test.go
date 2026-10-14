package repository_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

type cancellingGateSource struct {
	*bytes.Reader
	cancel context.CancelFunc
	reads  int
}

func (s *cancellingGateSource) Read(data []byte) (int, error) {
	s.reads++
	n, err := s.Reader.Read(data)
	s.cancel()
	return n, err
}

func TestArtifactGatePublicationStopsReadingOnCancellation(t *testing.T) {
	// Given: cancellation while the first encrypted bundle chunk is prepared.
	repo := newTestRepo(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	data := bytes.Repeat([]byte("x"), 2<<20)
	hash := sha256.Sum256(data)
	source := &cancellingGateSource{
		Reader: bytes.NewReader(data),
		cancel: cancel,
	}
	// When: publication observes cancellation between chunks.
	err = repo.SaveArtifactGate(ctx, db.CreateArtifactGateParams{
		DeploymentID: 1,
		BundleSize:   int64(len(data)),
		BundleSha256: hex.EncodeToString(hash[:]),
	}, source)
	// Then: cancellation returns before the remaining source is consumed.
	if !errors.Is(err, context.Canceled) || source.reads != 1 {
		t.Fatalf("publication consumed %d chunks after cancellation: %v",
			source.reads, err)
	}
}
