package handler

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"github.com/go-chi/chi/v5"
)

func (h *ArtifactGateHandler) openGateBundle(
	w http.ResponseWriter, r *http.Request,
) (*os.File, db.ArtifactGate) {
	w.Header().Set("Cache-Control", "no-store")
	u := auth.UserFromContext(r.Context())
	if u == nil || u.Role == "viewer" {
		gateHTTPError(w, r, 403, "Artifact download requires write access")
		return nil, db.ArtifactGate{}
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		gateHTTPError(w, r, 400, "Invalid deployment ID")
		return nil, db.ArtifactGate{}
	}
	index, err := strconv.ParseInt(chi.URLParam(r, "stepIndex"), 10, 64)
	if err != nil || index < 0 {
		gateHTTPError(w, r, 400, "Invalid step index")
		return nil, db.ArtifactGate{}
	}
	gate, err := h.repo.Queries.GetArtifactGate(
		r.Context(),
		db.GetArtifactGateParams{DeploymentID: id, StepIndex: index},
	)
	if err != nil {
		gateHTTPError(w, r, 404, "Artifact not found")
		return nil, db.ArtifactGate{}
	}
	now, err := h.repo.Queries.CurrentUnixTime(r.Context())
	if err != nil || gate.ExpiresAt <= now {
		gateHTTPError(w, r, 409, "Artifact is expired")
		return nil, db.ArtifactGate{}
	}
	file, err := artifact.OpenGateSpool()
	if err != nil {
		gateHTTPError(w, r, 500, "Artifact storage unavailable")
		return nil, db.ArtifactGate{}
	}
	if err := h.repo.WriteArtifactGateBundle(
		r.Context(),
		h.repo.Queries,
		gate,
		file,
	); err != nil {
		file.Close()
		gateHTTPError(w, r, 409, "Artifact is unavailable or changed")
		return nil, db.ArtifactGate{}
	}
	if _, err := file.Seek(0, 0); err != nil {
		file.Close()
		gateHTTPError(w, r, 500, "Artifact storage unavailable")
		return nil, db.ArtifactGate{}
	}
	return file, gate
}

func (h *ArtifactGateHandler) Download(w http.ResponseWriter, r *http.Request) {
	file, gate := h.openGateBundle(w, r)
	if file == nil {
		return
	}
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err != nil {
			gateHTTPError(w, r, 409, "Artifact is unavailable")
			return
		}
		if header.Name != gate.ArtifactPath {
			continue
		}
		// Verify the selected artifact before releasing any bytes to the client.
		hash := sha256.New()
		if _, err := io.Copy(
			hash,
			reader,
		); err != nil ||
			hex.EncodeToString(hash.Sum(nil)) != gate.ArtifactSha256 {
			gateHTTPError(w, r, 409, "Artifact is changed")
			return
		}
		if _, err := file.Seek(0, 0); err != nil {
			gateHTTPError(w, r, 500, "Artifact storage unavailable")
			return
		}
		reader = tar.NewReader(file)
		for {
			header, err := reader.Next()
			if err != nil {
				gateHTTPError(w, r, 409, "Artifact is unavailable")
				return
			}
			if header.Name != gate.ArtifactPath {
				continue
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().
				Set("Content-Disposition", `attachment; filename="approved-artifact.bin"`)
			w.Header().Set("Content-Length", strconv.FormatInt(header.Size, 10))
			if _, err := io.Copy(w, reader); err != nil {
				return
			}
			return
		}
	}
}
