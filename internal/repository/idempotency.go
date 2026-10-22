package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"durpdeploy/internal/db"
)

var ErrIdempotencyConflict = errors.New(
	"idempotency key was already used with a different request")

type IdempotencyRequest struct {
	db.ClaimIdempotencyParams
}

func NewIdempotencyRequest(userID, projectID int64,
	operation, key string, request any,
) (IdempotencyRequest, error) {
	if key == "" {
		return IdempotencyRequest{}, nil
	}
	body, err := json.Marshal(request)
	if err != nil {
		return IdempotencyRequest{}, err
	}
	keyHash, requestHash := sha256.Sum256([]byte(key)), sha256.Sum256(body)
	return IdempotencyRequest{db.ClaimIdempotencyParams{
		UserID: userID, ProjectID: projectID, Operation: operation,
		KeyHash:     hex.EncodeToString(keyHash[:]),
		RequestHash: hex.EncodeToString(requestHash[:]),
	}}, nil
}

func (r *Repository) LookupIdempotency(ctx context.Context,
	request IdempotencyRequest,
) (json.RawMessage, error) {
	if request.KeyHash == "" {
		return nil, nil
	}
	row, err := r.Queries.GetIdempotency(ctx, db.GetIdempotencyParams{
		UserID: request.UserID, ProjectID: request.ProjectID,
		Operation: request.Operation, KeyHash: request.KeyHash,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.RequestHash != request.RequestHash {
		return nil, ErrIdempotencyConflict
	}
	return json.RawMessage(row.ResponseJson), nil
}

// Claim and Complete must share the resource's transaction. A conflicting
// insert waits for its owner to commit or roll back on every supported backend.
func (request IdempotencyRequest) Claim(ctx context.Context,
	q *db.Queries,
) error {
	if request.KeyHash == "" {
		return nil
	}
	return q.ClaimIdempotency(ctx, request.ClaimIdempotencyParams)
}

func (request IdempotencyRequest) Complete(ctx context.Context,
	q *db.Queries, response any,
) error {
	if request.KeyHash == "" {
		return nil
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return q.CompleteIdempotency(ctx, db.CompleteIdempotencyParams{
		ResponseJson: string(body), UserID: request.UserID,
		ProjectID: request.ProjectID, Operation: request.Operation,
		KeyHash: request.KeyHash,
	})
}

// WithCreationTx retries the complete transaction on SQLite write contention.
func (r *Repository) WithCreationTx(ctx context.Context,
	fn func(*db.Queries) error,
) error {
	return withSQLiteBusyRetry(ctx, func() error { return r.WithTx(ctx, fn) })
}
