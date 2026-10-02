package agentserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"durpdeploy/internal/db"
	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Artifact serves verified bytes, never a repository credential or upstream URL.
// The mTLS boundary and a live, token-bound step claim are both required.
func (s *Server) Artifact(w http.ResponseWriter, r *http.Request) {
	id, ok := deploymentID(w, r)
	if !ok {
		return
	}
	agentID, ok := AgentIDFromContext(r.Context())
	if !ok {
		w.WriteHeader(401)
		return
	}
	var request struct {
		ClaimToken agentproto.ClaimToken `json:"claim_token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ClaimToken == "" {
		w.WriteHeader(400)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		w.WriteHeader(400)
		return
	}
	claim := db.HasLiveArtifactClaimParams{
		DeploymentID:   id,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(request.ClaimToken),
	}
	if !s.artifactClaimLive(w, r, claim) {
		return
	}
	download, err := s.repository.DownloadDeploymentArtifact(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(502)
		return
	}
	defer func() {
		if err := os.Remove(download.Path); err != nil {
			slog.ErrorContext(
				r.Context(),
				"remove artifact download",
				"deployment_id",
				id,
				"err",
				err,
			)
		}
	}()
	if !s.artifactClaimLive(w, r, claim) {
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="artifact.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Artifact-SHA256", download.SHA256)
	w.Header().Set("X-Artifact-Size", strconv.FormatInt(download.Size, 10))
	http.ServeFile(w, r, download.Path)
}

func (s *Server) artifactClaimLive(
	w http.ResponseWriter,
	r *http.Request,
	claim db.HasLiveArtifactClaimParams,
) bool {
	now, err := s.repository.Queries.CurrentUnixTime(r.Context())
	if err != nil {
		w.WriteHeader(500)
		return false
	}
	claim.Now = nullableInt64(now)
	claim.StaleBefore = nullableInt64(
		now - int64(agentproto.LostThreshold.Seconds()),
	)
	allowed, err := s.repository.Queries.HasLiveArtifactClaim(
		r.Context(),
		claim,
	)
	if err != nil {
		w.WriteHeader(500)
		return false
	}
	if allowed != 1 {
		w.WriteHeader(409)
		return false
	}
	return true
}
