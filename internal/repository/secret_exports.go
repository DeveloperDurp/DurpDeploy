package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"durpdeploy/internal/db"
)

var ErrSecretExportInvalid = errors.New("export is unavailable or not approved")

var ErrSecretExportScope = errors.New(
	"scope must be project or lifecycle with a positive scope_id",
)

type SecretExportDocument struct {
	Scope     string           `json:"scope"`
	ScopeID   int64            `json:"scope_id"`
	Variables []ExportedSecret `json:"variables"`
}

type ExportedSecret struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
}

// SecretExportHash keeps claim credentials and MFA tokens out of stored state.
func SecretExportHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (r *Repository) SecretExportScopeName(
	ctx context.Context,
	scope string,
	id int64,
) (string, error) {
	if id <= 0 {
		return "", ErrSecretExportScope
	}
	switch scope {
	case "project":
		row, err := r.Queries.GetProject(ctx, id)
		return row.Name, err
	case "lifecycle":
		row, err := r.Queries.GetLifecycle(ctx, id)
		return row.Name, err
	default:
		return "", ErrSecretExportScope
	}
}

func (r *Repository) NewSecretExport(
	ctx context.Context,
	input db.CreateSecretExportParams,
) (db.SecretExport, string, error) {
	if _, err := r.SecretExportScopeName(ctx, input.Scope, input.ScopeID); err != nil {
		return db.SecretExport{}, "", err
	}
	now := time.Now()
	if err := r.Queries.DeleteExpiredSecretExports(ctx, now.Unix()); err != nil {
		return db.SecretExport{}, "", err
	}
	input.ID = rand.Text()
	input.ExpiresAt = now.Add(5 * time.Minute).Unix()
	claim := ""
	if input.TokenID != "" {
		claim = rand.Text()
		input.ClaimHash = SecretExportHash(claim)
	}
	row, err := r.Queries.CreateSecretExport(ctx, input)
	return row, claim, err
}

func (r *Repository) SecretExport(
	ctx context.Context,
	id string,
	userID int64,
) (db.SecretExport, error) {
	row, err := r.Queries.GetSecretExport(ctx, db.GetSecretExportParams{
		ID: id, UserID: userID, ExpiresAt: time.Now().Unix(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrSecretExportInvalid
	}
	return row, err
}

// TakeSecretExport consumes approval before returning any plaintext. Its caller
// supplies either the bound browser session or the original API token and claim.
func (r *Repository) TakeSecretExport(
	ctx context.Context,
	id string,
	userID int64,
	sessionID, tokenID, claim string,
) (SecretExportDocument, error) {
	var result SecretExportDocument
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			row, err := consumeSecretExport(
				ctx,
				q,
				id,
				userID,
				sessionID,
				tokenID,
				claim,
			)
			if err != nil {
				return err
			}
			result, err = r.secretExportDocument(ctx, q, row)
			return err
		})
	})
	if err != nil {
		return SecretExportDocument{}, err
	}
	return result, nil
}

func secretExportCredentialMatches(
	row db.SecretExport,
	sessionID, tokenID, claim string,
) bool {
	if tokenID != "" {
		return row.TokenID == tokenID && row.ClaimHash != "" &&
			subtle.ConstantTimeCompare(
				[]byte(row.ClaimHash),
				[]byte(SecretExportHash(claim)),
			) == 1
	}
	return row.TokenID == "" && row.SessionID == sessionID && sessionID != ""
}

func consumeSecretExport(
	ctx context.Context,
	q *db.Queries,
	id string,
	userID int64,
	sessionID, tokenID, claim string,
) (db.SecretExport, error) {
	now := time.Now().Unix()
	row, err := q.GetSecretExport(ctx, db.GetSecretExportParams{
		ID: id, UserID: userID, ExpiresAt: now,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrSecretExportInvalid
	}
	if err != nil {
		return row, err
	}
	if row.Approved != 1 ||
		!secretExportCredentialMatches(row, sessionID, tokenID, claim) {
		return row, ErrSecretExportInvalid
	}
	count, err := q.ConsumeSecretExport(ctx, db.ConsumeSecretExportParams{
		ID: row.ID, UserID: userID, SessionID: row.SessionID, ExpiresAt: now, Now: now,
	})
	if err != nil {
		return row, err
	}
	if count != 1 {
		return row, ErrSecretExportInvalid
	}
	return row, nil
}

func (r *Repository) secretExportDocument(
	ctx context.Context,
	q *db.Queries,
	row db.SecretExport,
) (SecretExportDocument, error) {
	result := SecretExportDocument{
		Scope:     row.Scope,
		ScopeID:   row.ScopeID,
		Variables: []ExportedSecret{},
	}
	if row.Scope == "project" {
		rows, err := q.ListVariablesByProject(ctx, row.ScopeID)
		if err != nil {
			return result, err
		}
		for _, v := range rows {
			if err := r.addExportedSecret(&result, v.ID, v.Name, v.Value, v.EnvironmentID, v.Secret); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	rows, err := q.ListLifecycleVariables(ctx, row.ScopeID)
	if err != nil {
		return result, err
	}
	for _, v := range rows {
		if err := r.addExportedSecret(&result, v.ID, v.Name, v.Value, v.EnvironmentID, v.Secret); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (r *Repository) addExportedSecret(
	result *SecretExportDocument,
	id int64,
	name string,
	value sql.NullString,
	env sql.NullInt64,
	isSecret int64,
) error {
	if isSecret == 0 {
		return nil
	}
	plain, err := r.decryptValue(value)
	if err != nil {
		return err
	}
	item := ExportedSecret{ID: id, Name: name, Value: plain.String}
	if env.Valid {
		item.EnvironmentID = &env.Int64
	}
	result.Variables = append(result.Variables, item)
	return nil
}
