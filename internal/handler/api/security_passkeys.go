package api

import (
	"encoding/base64"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/repository"
)

type passkeyWarningResponse struct {
	CredentialID string `json:"credential_id"`
	Name         string `json:"name"`
	CloneWarning bool   `json:"clone_warning"`
}

type SecurityPasskeysHandler struct {
	repo *repository.Repository
}

func NewSecurityPasskeysHandler(
	repo *repository.Repository,
) *SecurityPasskeysHandler {
	return &SecurityPasskeysHandler{repo: repo}
}

// swagger:operation GET /security/passkeys security listOwnPasskeys
//
// List the token owner's passkey warning state. Project-scoped tokens are rejected.
// ---
// produces:
// - application/json
// security:
// - bearer: []
// responses:
//
//	'200':
//	  description: Owned passkey warning metadata
//	  schema:
//	    type: array
//	    items:
//	      type: object
//	      required: [credential_id, name, clone_warning]
//	      properties:
//	        credential_id:
//	          type: string
//	          description: Base64url credential identifier without padding
//	        name:
//	          type: string
//	        clone_warning:
//	          type: boolean
//	'401':
//	  description: Authentication required
//	'403':
//	  description: Token is restricted to selected projects
//	'500':
//	  description: Could not load passkeys
func (h *SecurityPasskeysHandler) List(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	rows, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		r.Context(), user.ID,
	)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not load passkeys",
		)
		return
	}
	items := make([]passkeyWarningResponse, len(rows))
	for i, row := range rows {
		items[i] = passkeyWarningResponse{
			CredentialID: base64.RawURLEncoding.EncodeToString(
				row.CredentialID,
			),
			Name:         row.Name,
			CloneWarning: row.CloneWarning == 1,
		}
	}
	RespondJSON(w, http.StatusOK, items)
}
