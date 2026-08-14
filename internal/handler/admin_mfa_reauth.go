package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

type adminMFAResetIntent struct {
	TargetUserID int64  `json:"target_user_id"`
	Reason       string `json:"reason"`
}

func (h *AuthHandler) finishSecurityReauthentication(
	w http.ResponseWriter,
	r *http.Request,
	session *db.Session,
) {
	if err := h.markCurrentSessionReauthenticated(r, session); err != nil {
		http.Error(
			w,
			"mark session reauthenticated",
			http.StatusInternalServerError,
		)
		return
	}
	targetID, reason, found, reset, err :=
		h.resumeAdminMFAResetContinuation(r, session)
	if err != nil {
		audit.Suppress(r)
		h.writeSecurityChallengeError(w, err)
		return
	}
	if !found {
		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
		return
	}
	if !reset {
		audit.Suppress(r)
		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
		return
	}
	audit.SetMFAAdminReset(r, targetID, reason)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *AuthHandler) resumeAdminMFAResetContinuation(
	r *http.Request,
	session *db.Session,
) (int64, string, bool, bool, error) {
	factors, err := h.mfaFactors()
	if err != nil {
		return 0, "", false, false, err
	}
	var targetID int64
	var reason string
	reset := false
	found, err := h.challengeService().ConsumeAdminMFAResetContinuation(
		r.Context(),
		session.UserID,
		session.ID,
		func(
			ctx context.Context,
			queries *db.Queries,
			ceremonyJSON string,
		) error {
			var intent adminMFAResetIntent
			if err := json.Unmarshal(
				[]byte(ceremonyJSON),
				&intent,
			); err != nil {
				return nil
			}
			canonicalReason, validReason := mfaResetReason(intent.Reason)
			if !validReason || canonicalReason != intent.Reason ||
				intent.TargetUserID <= 0 {
				return nil
			}
			current, err := queries.GetSession(ctx, db.GetSessionParams{
				ID: session.ID, ExpiresAt: time.Now().Unix(),
			})
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if current.UserID != session.UserID || current.Role != "admin" ||
				!auth.IsRecentlyAuthenticated(&db.Session{
					ReauthenticatedAt: current.ReauthenticatedAt,
				}) || current.UserID == intent.TargetUserID {
				return nil
			}
			if _, err := queries.GetUserByID(
				ctx,
				intent.TargetUserID,
			); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
			if err := factors.ResetWith(
				ctx,
				queries,
				intent.TargetUserID,
			); err != nil {
				return err
			}
			targetID = intent.TargetUserID
			reason = canonicalReason
			reset = true
			return nil
		},
	)
	if err != nil {
		return 0, "", found, false, err
	}
	return targetID, reason, found, reset, nil
}
