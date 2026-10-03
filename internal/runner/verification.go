package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/verification"
	"golang.org/x/net/idna"
)

func (r *DeploymentRunner) verifyDeployment(
	ctx, runCtx context.Context, deploymentID int64,
	environment map[string]string, secretValues []string, stage artifactStage,
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
		scrubber: NewScrubber(secretValues),
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
		target, decryptErr := r.repo.DecryptVerificationTarget(check.Target)
		if decryptErr != nil {
			err = decryptErr
			break
		}
		writer.scrubber = verificationHTTPScrubber(target, secretValues)
		err = verification.CheckHTTP(checkCtx, verification.Settings{
			Kind: verification.HTTP, Target: target,
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

func verificationHTTPScrubber(target string, secrets []string) *Scrubber {
	u, err := url.Parse(target)
	if err != nil {
		return NewScrubber(append(secrets, target))
	}
	secrets = append(secrets, target, u.RequestURI(), u.RawQuery, u.Host)
	hosts := []string{u.Hostname()}
	for _, convert := range []func(string) (string, error){
		idna.Lookup.ToASCII, idna.Lookup.ToUnicode,
	} {
		host, err := convert(u.Hostname())
		if err == nil && host != u.Hostname() {
			hosts = append(hosts, host)
		}
	}
	var patterns []string
	for _, host := range hosts {
		host = strings.TrimSuffix(host, ".")
		values := []string{host}
		if net.ParseIP(host) == nil {
			values = append(values, strings.Split(host, ".")...)
		}
		for _, value := range values {
			if value != "" {
				patterns = append(patterns, "(?i:"+regexp.QuoteMeta(value)+")")
			}
		}
	}
	values := strings.Split(u.Path, "/")
	for _, segment := range strings.Split(u.EscapedPath(), "/") {
		value, err := url.PathUnescape(segment)
		if err == nil && value != "" {
			secrets = append(secrets, segment)
			values = append(values, value)
		}
	}
	for _, value := range values {
		if value != "" {
			secrets = append(secrets, value,
				url.QueryEscape(value), url.PathEscape(value))
		}
	}
	for _, values := range u.Query() {
		for _, value := range values {
			secrets = append(secrets, value,
				url.QueryEscape(value), url.PathEscape(value))
		}
	}
	for _, pair := range strings.Split(u.RawQuery, "&") {
		if _, value, found := strings.Cut(pair, "="); found {
			secrets = append(secrets, value)
		}
	}
	return NewScrubber(secrets, patterns...)
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
	script, err := r.repo.VerificationStepScript(ctx, r.repo.Queries, frozen)
	if err != nil {
		return fmt.Errorf("decrypt verification step: %w", err)
	}
	step := deploymentStep{
		Name:            frozen.Name,
		ScriptBody:      script,
		Interpreter:     "bash",
		ExecutionTarget: "local",
		ContainerImage:  verification.BashImage,
		TimeoutSeconds:  frozen.TimeoutSeconds,
	}
	if err := json.Unmarshal([]byte(frozen.VariableNames),
		&step.VariableNames); err != nil {
		return fmt.Errorf("load verification variables: %w", err)
	}
	recorded, err := r.repo.Queries.RecordContainerNamespace(ctx,
		db.RecordContainerNamespaceParams{
			DeploymentID: deploymentID,
			Namespace:    sql.NullString{String: r.engine.scope(), Valid: true},
		})
	if err != nil {
		return fmt.Errorf("record verification container namespace: %w", err)
	}
	if recorded != 1 {
		return errContainerCleanup
	}
	err = r.runStepAttempt(runCtx, localStepAttempt{
		deploymentID: deploymentID, step: step, logWriter: writer,
		environment: environment, attempt: 1, artifact: stage,
		verification: true,
	})
	if runCtx.Err() != nil && !errors.Is(err, errContainerCleanup) {
		return runCtx.Err()
	}
	return err
}
