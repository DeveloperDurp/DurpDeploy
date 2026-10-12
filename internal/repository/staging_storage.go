package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/staging"
)

type StagingWrite struct {
	Key    db.GetStagingSnapshotParams
	Source *os.File
	Claim  *RemoteLifecycleClaim
}

// SaveStaging validates, canonicalizes and encrypts before acquiring DB locks.
// Publication and claim validation share the deployment lifecycle transaction.
func (r *Repository) SaveStaging(
	ctx context.Context,
	request StagingWrite,
) (result error) {
	if r.secrets == nil {
		return staging.ErrInvalid
	}
	snapshot, err := staging.Read(ctx, request.Source)
	if err != nil {
		return err
	}
	canonical, err := artifact.OpenGateSpool()
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, canonical.Close()) }()
	hash := sha256.New()
	if err := snapshot.Write(ctx, io.MultiWriter(canonical, hash)); err != nil {
		return err
	}
	size, err := canonical.Seek(0, io.SeekCurrent)
	if err != nil || size > staging.MaxArchive {
		return staging.ErrInvalid
	}
	if _, err := canonical.Seek(0, io.SeekStart); err != nil {
		return err
	}
	key := request.Key
	checksum := hex.EncodeToString(hash.Sum(nil))
	encrypted, err := artifact.OpenGateSpool()
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, encrypted.Close()) }()
	encoder := json.NewEncoder(encrypted)
	buffer := make([]byte, gateChunkSize)
	for index, remaining := int64(0), size; remaining > 0; index++ {
		n, err := io.ReadFull(
			canonical,
			buffer[:min(int64(len(buffer)), remaining)],
		)
		if err != nil {
			return err
		}
		ciphertext, err := r.secrets.Encrypt(
			stagingPrefix(key, index) + string(buffer[:n]),
		)
		if err != nil {
			return err
		}
		if err := encoder.Encode(ciphertext); err != nil {
			return err
		}
		remaining -= int64(n)
	}
	return r.WithDeploymentTx(
		ctx,
		key.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			deployment, err := q.GetDeployment(ctx, key.DeploymentID)
			if err != nil {
				return err
			}
			if deployment.Status != "running" {
				return ErrRemoteLifecycleConflict
			}
			if request.Claim != nil {
				run, err := liveStagingRun(ctx, q, *request.Claim)
				if err != nil {
					return err
				}
				if run.State != "started" || run.StepIndex != key.StepIndex ||
					run.AgentID != key.Producer {
					return ErrRemoteLifecycleConflict
				}
			}
			prior, err := q.GetStagingSnapshot(ctx, key)
			if err == nil {
				if prior.Sha256 == checksum && prior.Size == size {
					return nil
				}
				return ErrRemoteLifecycleConflict
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := q.CreateStagingSnapshot(ctx, db.CreateStagingSnapshotParams{
				DeploymentID: key.DeploymentID, StepIndex: key.StepIndex, Producer: key.Producer, Sha256: checksum, Size: size,
			}); err != nil {
				return err
			}
			if _, err := encrypted.Seek(0, io.SeekStart); err != nil {
				return err
			}
			decoder := json.NewDecoder(encrypted)
			for index, remaining := int64(0), size; remaining > 0; index++ {
				var ciphertext string
				if err := decoder.Decode(&ciphertext); err != nil {
					return err
				}
				if err := q.CreateStagingChunk(ctx, db.CreateStagingChunkParams{
					DeploymentID: key.DeploymentID, StepIndex: key.StepIndex, Producer: key.Producer, ChunkIndex: index, Ciphertext: ciphertext,
				}); err != nil {
					return err
				}
				remaining -= min(int64(gateChunkSize), remaining)
			}
			return nil
		},
	)
}

func stagingPrefix(key db.GetStagingSnapshotParams, index int64) string {
	return fmt.Sprintf(
		"staging/%d/%d/%s/%d\n",
		key.DeploymentID,
		key.StepIndex,
		key.Producer,
		index,
	)
}

// LoadStaging returns an unlinked spool. The caller must close it.
func (r *Repository) LoadStaging(
	ctx context.Context,
	key db.GetStagingSnapshotParams,
) (*os.File, error) {
	if r.secrets == nil {
		return nil, staging.ErrInvalid
	}
	meta, err := r.Queries.GetStagingSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	file, err := artifact.OpenGateSpool()
	if err != nil {
		return nil, err
	}
	if err := r.writeStaging(ctx, meta, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func (r *Repository) writeStaging(
	ctx context.Context,
	meta db.DeploymentStagingSnapshot,
	target io.Writer,
) error {
	if meta.Size <= 0 || meta.Size > staging.MaxArchive {
		return staging.ErrInvalid
	}
	key := db.GetStagingSnapshotParams{
		DeploymentID: meta.DeploymentID,
		StepIndex:    meta.StepIndex,
		Producer:     meta.Producer,
	}
	hash := sha256.New()
	writer := io.MultiWriter(target, hash)
	for index, remaining := int64(0), meta.Size; remaining > 0; index++ {
		ciphertext, err := r.Queries.GetStagingChunk(
			ctx,
			db.GetStagingChunkParams{
				DeploymentID: key.DeploymentID, StepIndex: key.StepIndex, Producer: key.Producer, ChunkIndex: index,
			},
		)
		if err != nil || len(ciphertext) > 2*gateChunkSize {
			return staging.ErrInvalid
		}
		plaintext, err := r.secrets.Decrypt(ciphertext)
		prefix := stagingPrefix(key, index)
		if err != nil || !strings.HasPrefix(plaintext, prefix) {
			return staging.ErrInvalid
		}
		data := plaintext[len(prefix):]
		if int64(len(data)) != min(int64(gateChunkSize), remaining) {
			return staging.ErrInvalid
		}
		if _, err := io.WriteString(writer, data); err != nil {
			return err
		}
		remaining -= int64(len(data))
	}
	if hex.EncodeToString(hash.Sum(nil)) != meta.Sha256 {
		return staging.ErrInvalid
	}
	return nil
}
