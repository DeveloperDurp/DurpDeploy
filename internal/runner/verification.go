package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/verification"
)

func (r *DeploymentRunner) verifyDeployment(
	ctx, runCtx context.Context, deploymentID int64,
	environment map[string]string, scrubber *Scrubber, stage artifactStage,
) error {
	check, err := r.repo.Queries.GetDeploymentVerification(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load verification: %w", err)
	}
	if check.Type == string(verification.HTTP) {
		if !r.beginLocalWork() {
			return context.Canceled
		}
		defer r.localWork.Done()
	}
	started, err := r.repo.Queries.StartDeploymentVerification(
		ctx,
		deploymentID,
	)
	if err != nil {
		return fmt.Errorf("start verification: %w", err)
	}
	if started != 1 {
		return errors.New("verification has already started")
	}
	writer := &broadcastWriter{
		broker: r.broker, repo: r.repo, deploymentID: deploymentID,
		stepName: "Post-deployment verification", ctx: ctx,
		scrubber: scrubber,
	}
	defer writer.Flush()
	if _, err := fmt.Fprintln(writer, "Verification started"); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(runCtx,
		time.Duration(check.TimeoutSeconds)*time.Second)
	defer cancel()
	switch verification.Kind(check.Type) {
	case verification.HTTP:
		err = verification.CheckHTTP(checkCtx, verification.Settings{
			Kind: verification.HTTP, Target: check.Target,
			TimeoutSeconds: check.TimeoutSeconds,
		}, writer)
	case verification.Bash:
		err = r.verifyBash(ctx, runCtx, deploymentID, check.StepIndex,
			writer, environment, stage)
	default:
		err = errors.New("unknown verification type")
	}
	if check.Type == string(verification.HTTP) && checkCtx.Err() != nil {
		err = checkCtx.Err()
	}
	return r.finishVerification(ctx, runCtx, deploymentID, writer, err)
}

func (r *DeploymentRunner) finishVerification(
	ctx, runCtx context.Context, deploymentID int64,
	writer *broadcastWriter, err error,
) error {
	if err != nil {
		message := "Verification failed"
		if errors.Is(err, context.DeadlineExceeded) {
			message = "Verification timed out"
		}
		if _, writeErr := fmt.Fprintln(writer, message); writeErr != nil {
			err = errors.Join(err, writeErr)
		}
	}
	status := "succeeded"
	if err != nil {
		status = "failed"
		if errors.Is(err, errDeploymentCancelled) ||
			(errors.Is(err, context.Canceled) && runCtx.Err() != nil) {
			status = "cancelled"
		}
	}
	if _, writeErr := fmt.Fprintf(
		writer,
		"Verification %s\n",
		status,
	); writeErr != nil {
		err = errors.Join(err, writeErr)
	}
	if finishErr := r.repo.Queries.FinishDeploymentVerification(ctx,
		db.FinishDeploymentVerificationParams{
			Status: status, DeploymentID: deploymentID,
		}); finishErr != nil {
		return errors.Join(
			err,
			fmt.Errorf("finish verification: %w", finishErr),
		)
	}
	audit.Record(ctx, r.repo, audit.Entry{
		Action: "verification_" + status, EntityType: "deployment",
		EntityID: sql.NullInt64{Int64: deploymentID, Valid: true},
	})
	return err
}

func (r *DeploymentRunner) verifyBash(
	ctx, runCtx context.Context, deploymentID, index int64,
	writer *broadcastWriter, environment map[string]string, stage artifactStage,
) error {
	steps, err := r.repo.Queries.ListDeploymentSteps(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("load verification step: %w", err)
	}
	if index < 0 || index >= int64(len(steps)) {
		return errors.New("verification step is missing")
	}
	frozen := steps[index]
	step := deploymentStep{
		Name:            frozen.Name,
		ScriptBody:      frozen.ScriptBody,
		Interpreter:     frozen.Interpreter,
		ExecutionTarget: frozen.ExecutionTarget,
		ContainerImage:  frozen.ContainerImage,
		TimeoutSeconds:  frozen.TimeoutSeconds,
	}
	if err := json.Unmarshal([]byte(frozen.VariableNames),
		&step.VariableNames); err != nil {
		return fmt.Errorf("load verification variables: %w", err)
	}
	if step.ExecutionTarget == "agent" {
		return r.runRemoteStep(ctx, runCtx, remoteStepRequest{
			deploymentID: deploymentID, stepIndex: index, step: step,
			logWriter: writer,
		})
	}
	err = r.runStepAttempt(runCtx, localStepAttempt{
		deploymentID: deploymentID, step: step, logWriter: writer,
		environment: environment, attempt: 1, artifact: stage,
	})
	if runCtx.Err() != nil && !errors.Is(err, errContainerCleanup) {
		return runCtx.Err()
	}
	return err
}
