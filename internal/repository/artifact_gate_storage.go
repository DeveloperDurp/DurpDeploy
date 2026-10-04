package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

const gateChunkSize = 1 << 20

var ErrArtifactGate = errors.New(
	"artifact approval is unavailable, changed, or expired",
)

// SaveArtifactGate stores encrypted chunks in the same durable database as
// approval identity. A commit precedes deletion of the temporary staging area.
func (r *Repository) SaveArtifactGate(
	ctx context.Context,
	gate db.CreateArtifactGateParams,
	source io.Reader,
) error {
	if r.secrets == nil || gate.BundleSize <= 0 ||
		gate.BundleSize > artifact.MaxDownload {
		return ErrArtifactGate
	}
	return r.WithTx(ctx, func(q *db.Queries) error {
		changed, err := q.PublishArtifactGateDeployment(ctx, gate.DeploymentID)
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrArtifactGate
		}
		if err := q.CreateArtifactGate(ctx, gate); err != nil {
			return err
		}
		hash := sha256.New()
		buffer := make([]byte, gateChunkSize)
		remaining := gate.BundleSize
		for index := int64(0); remaining > 0; index++ {
			n, err := io.ReadFull(
				source,
				buffer[:min(int64(len(buffer)), remaining)],
			)
			if err != nil {
				return err
			}
			if _, err := hash.Write(buffer[:n]); err != nil {
				return err
			}
			prefix := fmt.Sprintf(
				"%d/%d/%d\n",
				gate.DeploymentID,
				gate.StepIndex,
				index,
			)
			ciphertext, err := r.secrets.Encrypt(prefix + string(buffer[:n]))
			if err != nil {
				return err
			}
			if err := q.CreateArtifactGateChunk(
				ctx,
				db.CreateArtifactGateChunkParams{
					DeploymentID: gate.DeploymentID, StepIndex: gate.StepIndex,
					ChunkIndex: index, Ciphertext: ciphertext,
				},
			); err != nil {
				return err
			}
			remaining -= int64(n)
		}
		if hex.EncodeToString(hash.Sum(nil)) != gate.BundleSha256 {
			return ErrArtifactGate
		}
		return nil
	})
}

func (r *Repository) WriteArtifactGateBundle(
	ctx context.Context,
	q *db.Queries,
	gate db.ArtifactGate,
	target io.Writer,
) error {
	if r.secrets == nil || gate.BundleSize <= 0 ||
		gate.BundleSize > artifact.MaxDownload {
		return ErrArtifactGate
	}
	remaining := gate.BundleSize
	hash := sha256.New()
	writer := io.MultiWriter(target, hash)
	for index := int64(0); remaining > 0; index++ {
		ciphertext, err := q.GetArtifactGateChunk(
			ctx,
			db.GetArtifactGateChunkParams{
				DeploymentID: gate.DeploymentID,
				StepIndex:    gate.StepIndex,
				ChunkIndex:   index,
			},
		)
		if err != nil || len(ciphertext) > 2*gateChunkSize {
			return ErrArtifactGate
		}
		plaintext, err := r.secrets.Decrypt(ciphertext)
		if err != nil {
			return ErrArtifactGate
		}
		prefix := fmt.Sprintf(
			"%d/%d/%d\n",
			gate.DeploymentID,
			gate.StepIndex,
			index,
		)
		if !strings.HasPrefix(plaintext, prefix) {
			return ErrArtifactGate
		}
		data := plaintext[len(prefix):]
		if int64(len(data)) != min(int64(gateChunkSize), remaining) {
			return ErrArtifactGate
		}
		if _, err := io.WriteString(writer, data); err != nil {
			return err
		}
		remaining -= int64(len(data))
	}
	if hex.EncodeToString(hash.Sum(nil)) != gate.BundleSha256 {
		return ErrArtifactGate
	}
	return nil
}
