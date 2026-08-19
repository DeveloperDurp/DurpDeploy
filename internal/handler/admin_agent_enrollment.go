package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

var errAgentNotPending = errors.New("agent is not pending")

func (h *AgentAdminHandler) CreateEnrollment(
	w http.ResponseWriter,
	r *http.Request,
) {
	response, err := h.issueEnrollment(
		r.Context(),
		chi.URLParam(r, "agentID"),
		false,
	)
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "agent not found")
		return
	}
	if errors.Is(err, errAgentNotPending) {
		writeAdminError(w, http.StatusConflict, "agent must be pending")
		return
	}
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not create enrollment",
		)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if wantsHTML(r) {
		agent, ok := h.agentPageAgent(w, r)
		if !ok {
			return
		}
		w.WriteHeader(http.StatusCreated)
		if err := pages.AgentEnrollmentPage(pages.AgentEnrollmentView{
			Agent: agent, Token: response.Token, ExpiresAt: response.ExpiresAt,
			CurrentPath: r.URL.Path,
		}).Render(r.Context(), w); err != nil {
			http.Error(
				w,
				"could not render enrollment",
				http.StatusInternalServerError,
			)
			return
		}
		return
	}
	writeAdminJSON(w, http.StatusCreated, response)
}

func (h *AgentAdminHandler) EnrollmentPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	agent, ok := h.agentPageAgent(w, r)
	if !ok {
		return
	}
	if err := pages.AgentEnrollmentPage(pages.AgentEnrollmentView{
		Agent: agent, CurrentPath: r.URL.Path,
	}).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"could not render enrollment form",
			http.StatusInternalServerError,
		)
	}
}

func (h *AgentAdminHandler) ReenrollAgent(
	w http.ResponseWriter,
	r *http.Request,
) {
	if _, ok := h.agent(w, r); !ok {
		return
	}
	response, err := h.issueEnrollment(
		r.Context(),
		chi.URLParam(r, "agentID"),
		true,
	)
	if errors.Is(err, errAgentNotPending) {
		writeAdminError(w, http.StatusConflict, "agent must be revoked")
		return
	}
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not re-enroll agent",
		)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAdminJSON(w, http.StatusCreated, response)
}

func mintEnrollmentToken() (string, string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", nil, err
	}
	token := "ddp_enroll_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, token[:15], hash[:], nil
}

func (h *AgentAdminHandler) issueEnrollment(
	ctx context.Context,
	agentID string,
	reenroll bool,
) (enrollmentResponse, error) {
	token, prefix, hash, err := mintEnrollmentToken()
	if err != nil {
		return enrollmentResponse{}, err
	}
	expiresAt := time.Now().Add(15 * time.Minute).Unix()
	err = h.repo.WithTx(ctx, func(queries *db.Queries) error {
		if reenroll {
			updated, err := queries.ReenrollAgent(ctx, db.ReenrollAgentParams{
				ID:        agentID,
				UpdatedAt: time.Now().Unix(),
			})
			if err != nil {
				return err
			}
			if updated != 1 {
				return errAgentNotPending
			}
		} else {
			agent, err := queries.GetAgent(ctx, agentID)
			if err != nil {
				return err
			}
			if agent.Status != "pending" {
				return errAgentNotPending
			}
		}
		if err := queries.RevokeAgentEnrollmentTokens(
			ctx,
			db.RevokeAgentEnrollmentTokensParams{
				AgentID:   agentID,
				RevokedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
			},
		); err != nil {
			return err
		}
		return queries.CreateAgentEnrollmentToken(
			ctx,
			db.CreateAgentEnrollmentTokenParams{
				AgentID:     agentID,
				TokenHash:   hash,
				TokenPrefix: prefix,
				ExpiresAt:   expiresAt,
			},
		)
	})
	if err != nil {
		return enrollmentResponse{}, err
	}
	return enrollmentResponse{
		AgentID:   agentID,
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}
