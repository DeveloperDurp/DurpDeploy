package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

type Repository struct {
	DB      *sql.DB
	Queries *db.Queries
}

func New(dbConn *sql.DB) *Repository {
	return &Repository{
		DB:      dbConn,
		Queries: db.New(dbConn),
	}
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
