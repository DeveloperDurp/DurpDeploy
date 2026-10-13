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
			n, copyErr := io.Copy(file, io.LimitReader(stdout, staging.MaxArchive+1))
			closeErr := stdout.Close()
			waitErr := command.Wait()
			if err := errors.Join(copyErr, closeErr, waitErr); err != nil {
				return err
			}
			if n > staging.MaxArchive {
				return staging.ErrInvalid
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
	baseFile, err := r.repo.LoadStaging(ctx, key)
	if err != nil {
		return nil, err
	}
	files = append(files, baseFile)
	base, err := staging.Read(ctx, baseFile)
	if err != nil {
		return nil, err
	}
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
	outputs := make([]staging.Snapshot, 0, len(runs))
	for _, run := range runs {
		if run.State != "succeeded" {
			return nil, repository.ErrRemoteLifecycleConflict
		}
		key.Producer = run.AgentID
		file, err := r.repo.LoadStaging(ctx, key)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
		output, err := staging.Read(ctx, file)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	merged, err := staging.Merge(base, outputs)
	if err != nil {
		return nil, err
	}
	result, err = artifact.OpenGateSpool()
	if err != nil {
		return nil, err
	}
	if err := merged.Write(ctx, result); err != nil {
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
