package repository

import (
	"context"
	"database/sql"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

type Repository struct {
	DB      *sql.DB
	Queries *db.Queries
	secrets *secret.Box
}

func New(dbConn *sql.DB) *Repository {
	return &Repository{
		DB:      dbConn,
		Queries: db.New(dbConn),
	}
}

// SetSecretBox configures the AES-GCM box used to encrypt/decrypt the
// `value` column of variables/release_variables (P1-3). Until this is
// called, values are stored/read as plaintext — production startup
// (cmd/server/main.go) refuses to boot without one; tests that don't
// exercise the encryption path may simply skip calling it.
func (r *Repository) SetSecretBox(box *secret.Box) {
	r.secrets = box
}

func (r *Repository) encryptValue(v sql.NullString) (sql.NullString, error) {
	if r.secrets == nil {
		return v, nil
	}
	return r.secrets.EncryptNullString(v)
}

func (r *Repository) decryptValue(v sql.NullString) (sql.NullString, error) {
	if r.secrets == nil {
		return v, nil
	}
	return r.secrets.DecryptNullString(v)
}

// EncryptValue exposes the encrypt step for callers that write
// variables/release_variables through a transaction-bound *db.Queries
// (e.g. release snapshot creation) instead of the wrapper methods below.
func (r *Repository) EncryptValue(v sql.NullString) (sql.NullString, error) {
	return r.encryptValue(v)
}

func (r *Repository) decryptVariable(v db.Variable) (db.Variable, error) {
	dv, err := r.decryptValue(v.Value)
	if err != nil {
		return db.Variable{}, fmt.Errorf("decrypt variable %d: %w", v.ID, err)
	}
	v.Value = dv
	return v, nil
}

func (r *Repository) decryptReleaseVariable(
	v db.ReleaseVariable,
) (db.ReleaseVariable, error) {
	dv, err := r.decryptValue(v.Value)
	if err != nil {
		return db.ReleaseVariable{}, fmt.Errorf(
			"decrypt release variable %d: %w", v.ID, err,
		)
	}
	v.Value = dv
	return v, nil
}

// CreateVariable encrypts arg.Value before insert and decrypts the row
// returned by the DB, so callers only ever see plaintext.
func (r *Repository) CreateVariable(
	ctx context.Context,
	arg db.CreateVariableParams,
) (db.Variable, error) {
	enc, err := r.encryptValue(arg.Value)
	if err != nil {
		return db.Variable{}, fmt.Errorf("encrypt variable value: %w", err)
	}
	arg.Value = enc
	v, err := r.Queries.CreateVariable(ctx, arg)
	if err != nil {
		return db.Variable{}, err
	}
	return r.decryptVariable(v)
}

// UpdateVariable encrypts arg.Value before update and decrypts the
// returned row.
func (r *Repository) UpdateVariable(
	ctx context.Context,
	arg db.UpdateVariableParams,
) (db.Variable, error) {
	enc, err := r.encryptValue(arg.Value)
	if err != nil {
		return db.Variable{}, fmt.Errorf("encrypt variable value: %w", err)
	}
	arg.Value = enc
	v, err := r.Queries.UpdateVariable(ctx, arg)
	if err != nil {
		return db.Variable{}, err
	}
	return r.decryptVariable(v)
}

// GetVariable returns the variable with its value decrypted.
func (r *Repository) GetVariable(ctx context.Context, id int64) (db.Variable, error) {
	v, err := r.Queries.GetVariable(ctx, id)
	if err != nil {
		return db.Variable{}, err
	}
	return r.decryptVariable(v)
}

// ListVariablesByProject returns variables with their values decrypted.
func (r *Repository) ListVariablesByProject(
	ctx context.Context,
	projectID int64,
) ([]db.Variable, error) {
	vars, err := r.Queries.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range vars {
		dv, err := r.decryptVariable(vars[i])
		if err != nil {
			return nil, err
		}
		vars[i] = dv
	}
	return vars, nil
}

// GetReleaseVariable returns a release variable with its value decrypted.
func (r *Repository) GetReleaseVariable(
	ctx context.Context,
	id int64,
) (db.ReleaseVariable, error) {
	v, err := r.Queries.GetReleaseVariable(ctx, id)
	if err != nil {
		return db.ReleaseVariable{}, err
	}
	return r.decryptReleaseVariable(v)
}

// ListReleaseVariablesByRelease returns release variables with their
// values decrypted. The runner relies on these plaintext values for
// env injection and log redaction.
func (r *Repository) ListReleaseVariablesByRelease(
	ctx context.Context,
	releaseID int64,
) ([]db.ReleaseVariable, error) {
	vars, err := r.Queries.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	for i := range vars {
		dv, err := r.decryptReleaseVariable(vars[i])
		if err != nil {
			return nil, err
		}
		vars[i] = dv
	}
	return vars, nil
}

// WithTx runs fn inside a DB transaction, passing it a *db.Queries bound to
// that transaction. The transaction is committed if fn returns nil, and
// rolled back otherwise.
func (r *Repository) WithTx(
	ctx context.Context,
	fn func(q *db.Queries) error,
) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(r.Queries.WithTx(tx)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
