package agentserver

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/staging"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (s *Server) AuditWrites(next http.Handler) http.Handler {
	return audit.Middleware(s.repository)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			agentID, _ := AgentIDFromContext(r.Context())
			audit.SetAgentActor(r, agentID)
			next.ServeHTTP(w, r)
		},
	))
}

func (s *Server) stagingClaim(
	w http.ResponseWriter,
	r *http.Request,
) (repository.RemoteLifecycleClaim, bool) {
	id, ok := deploymentID(w, r)
	if !ok {
		return repository.RemoteLifecycleClaim{}, false
	}
	agentID, ok := AgentIDFromContext(r.Context())
	if !ok {
		w.WriteHeader(401)
		return repository.RemoteLifecycleClaim{}, false
	}
	token := r.Header.Get("X-DurpDeploy-Claim")
	if token == "" || len(token) > 256 {
		w.WriteHeader(400)
		return repository.RemoteLifecycleClaim{}, false
	}
	return repository.RemoteLifecycleClaim{
		DeploymentID:   id,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(agentproto.ClaimToken(token)),
	}, true
}

func (s *Server) admitStagingTransfer(
	w http.ResponseWriter,
	r *http.Request,
	run db.RemoteStepRun,
) (func(), bool) {
	finish, ok := s.repository.TryBeginStagingTransfer(
		db.GetStagingSnapshotParams{
			DeploymentID: run.DeploymentID,
			StepIndex:    run.StepIndex, Producer: run.AgentID,
		},
	)
	if !ok {
		w.Header().Set("Retry-After", "1")
		// Do not drain a slow rejected HTTP/1 upload before sending 429.
		if r.ProtoMajor == 1 {
			w.Header().Set("Connection", "close")
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}
	return finish, ok
}

func (s *Server) DownloadStaging(w http.ResponseWriter, r *http.Request) {
	claim, ok := s.stagingClaim(w, r)
	if !ok {
		return
	}
	run, err := s.repository.LiveStagingRun(r.Context(), claim)
	if err != nil {
		writeStagingError(w, err)
		return
	}
	finishTransfer, ok := s.admitStagingTransfer(w, r, run)
	if !ok {
		return
	}
	defer finishTransfer()
	stream, err := newStagingIO(w, r, time.Duration(
		s.repository.ResourceLimits().Transfer.IdleTimeoutSeconds,
	)*time.Second)
	if err != nil {
		slog.WarnContext(r.Context(), "configure staging transfer", "err", err)
		writeStagingError(w, err)
		return
	}
	defer func() {
		if err := stream.finish(); err != nil {
			slog.WarnContext(r.Context(), "finish staging transfer", "err", err)
		}
	}()
	key := db.GetStagingSnapshotParams{
		DeploymentID: run.DeploymentID,
		StepIndex:    run.StepIndex,
	}
	file, err := s.repository.LoadStaging(r.Context(), key)
	if err != nil {
		writeStagingError(w, err)
		return
	}
	//nolint:errcheck // Read-only temporary file; publication already succeeded.
	defer file.Close()
	meta, err := s.repository.Queries.GetStagingSnapshot(r.Context(), key)
	if err != nil {
		writeStagingError(w, err)
		return
	}
	if _, err := s.repository.LiveStagingRun(r.Context(), claim); err != nil {
		writeStagingError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.Header().Set("X-Artifact-SHA256", meta.Sha256)
	if _, err := io.Copy(stream, file); err != nil {
		slog.WarnContext(r.Context(), "send staging snapshot", "err", err)
	}
}

func (s *Server) UploadStaging(w http.ResponseWriter, r *http.Request) {
	claim, ok := s.stagingClaim(w, r)
	if !ok {
		return
	}
	run, err := s.repository.LiveStagingRun(r.Context(), claim)
	if err != nil {
		writeStagingError(w, err)
		return
	}
	if run.State != "started" {
		w.WriteHeader(409)
		return
	}
	limit := s.repository.ResourceLimits().Staging.ArchiveBytes
	if r.ContentLength > limit {
		w.WriteHeader(413)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	finishTransfer, ok := s.admitStagingTransfer(w, r, run)
	if !ok {
		return
	}
	defer finishTransfer()
	stream, err := newStagingIO(w, r, time.Duration(
		s.repository.ResourceLimits().Transfer.IdleTimeoutSeconds,
	)*time.Second)
	if err != nil {
		slog.WarnContext(r.Context(), "configure staging transfer", "err", err)
		writeStagingError(w, err)
		return
	}
	defer func() {
		if err := stream.finish(); err != nil {
			slog.WarnContext(r.Context(), "finish staging transfer", "err", err)
		}
	}()
	file, err := artifact.OpenGateSpool()
	if err != nil {
		writeStagingError(w, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.WarnContext(r.Context(), "close staging spool", "err", err)
		}
	}()
	if _, err := io.Copy(file, stream); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(413)
		} else {
			w.WriteHeader(400)
		}
		return
	}
	err = s.repository.SaveStaging(r.Context(), repository.StagingWrite{
		Key: db.GetStagingSnapshotParams{
			DeploymentID: run.DeploymentID,
			StepIndex:    run.StepIndex,
			Producer:     run.AgentID,
		}, Source: file, Claim: &claim,
	})
	if err != nil {
		writeStagingError(w, err)
		return
	}
	w.WriteHeader(204)
}

func writeStagingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrRemoteLifecycleConflict):
		w.WriteHeader(409)
	case errors.Is(err, staging.ErrInvalid):
		w.WriteHeader(422)
	default:
		w.WriteHeader(500)
	}
}
