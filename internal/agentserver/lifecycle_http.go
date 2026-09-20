package agentserver

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/go-chi/chi/v5"
)

func deploymentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		w.WriteHeader(http.StatusConflict)
		return 0, false
	}
	return id, true
}

func claimTokenHash(token agentproto.ClaimToken) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func nullableInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func writeTransitionStatus(
	w http.ResponseWriter,
	changed int64,
	err error,
) bool {
	if err != nil {
		writePollErrorStatus(w, err)
		return false
	}
	if changed != 1 {
		w.WriteHeader(http.StatusConflict)
		return false
	}
	return true
}

func writePollErrorStatus(w http.ResponseWriter, err error) {
	if repository.IsSQLiteBusy(err) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

func writeLifecycleStatus(w http.ResponseWriter, err error) bool {
	if errors.Is(err, repository.ErrRemoteLifecycleConflict) {
		w.WriteHeader(http.StatusConflict)
		return false
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	return true
}

func writeJSON[T agentproto.HeartbeatResponse | agentproto.PollResponse](
	w http.ResponseWriter,
	response T,
) {
	payload, err := json.Marshal(response)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(payload)
}
