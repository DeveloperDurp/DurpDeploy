package runner

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/staging"
)

type remoteStagingInput struct {
	key     db.GetStagingSnapshotParams
	volume  artifactStage
	current *os.File
}

func (r *DeploymentRunner) replaceTransfer(id int64, next *os.File) error {
	r.mu.Lock()
	prior := r.transfers[id]
	if r.transfers == nil {
		r.transfers = make(map[int64]*os.File)
	}
	if next == nil {
		delete(r.transfers, id)
	} else {
		r.transfers[id] = next
	}
	r.mu.Unlock()
	if prior != nil {
		return prior.Close()
	}
	return nil
}

func (r *DeploymentRunner) prepareRemoteStaging(
	ctx context.Context,
	input remoteStagingInput,
) (result error) {
	file := input.current
	if file == nil {
		var err error
		file, err = artifact.OpenGateSpool()
		if err != nil {
			return err
		}
		defer func() {
			result = artifact.SpoolCleanupError(ctx, result, file.Close())
		}()
		if input.volume.volume == "" {
			if err := tar.NewWriter(file).Close(); err != nil {
				return err
			}
		} else {
			command := r.engine.command(ctx, "exec", input.volume.keeper, "tar", "-cf", "-", "-C", deploymentStageMount, ".")
			stdout, err := command.StdoutPipe()
			if err != nil {
				return err
			}
			if err := command.Start(); err != nil {
				return err
			}
			limit := r.repo.ResourceLimits().Staging.ArchiveBytes
			n, copyErr := io.Copy(file, io.LimitReader(stdout, limit+1))
			closeErr := stdout.Close()
			waitErr := command.Wait()
			if n > limit {
				copyErr = errors.Join(copyErr, staging.ErrInvalid)
			}
			if err := errors.Join(copyErr, closeErr, waitErr); err != nil {
				return err
			}
		}
	}
	return r.repo.SaveStaging(
		ctx,
		repository.StagingWrite{Key: input.key, Source: file},
	)
}

func (r *DeploymentRunner) mergeRemoteStaging(
	ctx context.Context,
	key db.GetStagingSnapshotParams,
) (result *os.File, resultErr error) {
	runs, err := r.stagingMergeInputs(ctx, key)
	if err != nil {
		return nil, err
	}
	var files []*os.File
	defer func() {
		for _, file := range files {
			resultErr = errors.Join(resultErr, file.Close())
		}
		if resultErr != nil && result != nil {
			resultErr = errors.Join(resultErr, result.Close())
			result = nil
		}
	}()
	baseFile, base, err := r.repo.LoadStagingSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	files = append(files, baseFile)
	content, err := artifact.OpenGateSpool()
	if err != nil {
		return nil, err
	}
	files = append(files, content)
	merger, err := staging.NewMergerWithLimits(
		ctx,
		base,
		content,
		r.repo.ResourceLimits().Staging,
		r.repo.ResourceLimits().Merge,
	)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		key.Producer = run.AgentID
		if err := r.mergeStagingOutput(ctx, key, merger); err != nil {
			return nil, err
		}
	}
	merged, err := merger.Finish(ctx)
	if err != nil {
		return nil, err
	}
	result, err = artifact.OpenGateSpool()
	if err != nil {
		return nil, err
	}
	if err := merged.Write(ctx, staging.LimitWriter(
		result, r.repo.ResourceLimits().Staging.ArchiveBytes,
	)); err != nil {
		return result, err
	}
	if _, err := result.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	if err := r.repo.Queries.DeleteStepStaging(ctx, db.DeleteStepStagingParams{DeploymentID: key.DeploymentID, StepIndex: key.StepIndex}); err != nil {
		return result, err
	}
	return result, nil
}

// Check canonical sizes before decrypting or spooling any merge input.
func (r *DeploymentRunner) stagingMergeInputs(
	ctx context.Context,
	key db.GetStagingSnapshotParams,
) ([]db.RemoteStepRun, error) {
	runs, err := r.repo.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{
			DeploymentID: key.DeploymentID,
			StepIndex:    key.StepIndex,
		},
	)
	if err != nil {
		return nil, err
	}
	limits := r.repo.ResourceLimits()
	if int64(len(runs)) > limits.Merge.AgentOutputs {
		return nil, staging.ErrMergeBudget
	}
	keys := []db.GetStagingSnapshotParams{key}
	for _, run := range runs {
		if run.State != "succeeded" {
			return nil, repository.ErrRemoteLifecycleConflict
		}
		output := key
		output.Producer = run.AgentID
		keys = append(keys, output)
	}
	var total int64
	for _, input := range keys {
		meta, err := r.repo.Queries.GetStagingSnapshot(ctx, input)
		if err != nil {
			return nil, err
		}
		if meta.Size < 1024 ||
			meta.Size > limits.Staging.ArchiveBytes {
			return nil, staging.ErrInvalid
		}
		if meta.Size > limits.Merge.ArchiveBytes-total {
			return nil, staging.ErrMergeBudget
		}
		total += meta.Size
	}
	return runs, nil
}

func (r *DeploymentRunner) mergeStagingOutput(
	ctx context.Context,
	key db.GetStagingSnapshotParams,
	merger *staging.Merger,
) (result error) {
	file, output, err := r.repo.LoadStagingSnapshot(ctx, key)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	return merger.Add(ctx, output)
}

func (r *DeploymentRunner) importRemoteStaging(
	ctx context.Context,
	volume artifactStage,
	file *os.File,
) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// Delete through find without following links written by a previous script.
	command := r.engine.command(
		ctx,
		"exec",
		"--interactive",
		volume.keeper,
		"sh",
		"-c",
		"find /stage -mindepth 1 -maxdepth 1 -exec rm -rf -- {} + && tar -xf - -C /stage",
	)
	command.Stdin = file
	if err := command.Run(); err != nil {
		return fmt.Errorf("import staging snapshot: %w", err)
	}
	return nil
}
