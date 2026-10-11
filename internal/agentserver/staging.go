package agentserver

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/staging"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (s *Server) AuditWrites(next http.Handler) http.Handler {
	return audit.Middleware(s.repository)(next)
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
	stream, err := newStagingIO(w, r)
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
	if r.ContentLength > staging.MaxArchive {
		w.WriteHeader(413)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, staging.MaxArchive)
	stream, err := newStagingIO(w, r)
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
	defer file.Close()
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
