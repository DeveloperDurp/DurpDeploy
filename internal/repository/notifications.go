package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
)

// UpdateProjectNotifications binds a retained Gotify token to its destination.
func (r *Repository) UpdateProjectNotifications(
	ctx context.Context,
	params db.UpdateProjectNotificationsParams,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			locked, err := q.LockProject(ctx, params.ID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			current, err := q.GetProject(ctx, params.ID)
			if err != nil {
				return err
			}
			update := params
			// Database collations must not treat a nonempty replacement as empty.
			update.GotifyToken.Valid = params.GotifyToken.Valid &&
				params.GotifyToken.String != ""
			update.ClearGotifyToken = gotifyTokenClearFlag(
				params.ClearGotifyToken, current.GotifyUrl,
				params.GotifyUrl, params.GotifyToken,
			)
			return q.UpdateProjectNotifications(ctx, update)
		})
	})
}

// UpdateGlobalNotifications binds a retained Gotify token to its destination.
func (r *Repository) UpdateGlobalNotifications(
	ctx context.Context,
	params db.UpdateGlobalNotificationsParams,
) (db.GlobalNotification, error) {
	return withSQLiteBusyRetryValue(ctx, func() (db.GlobalNotification, error) {
		var settings db.GlobalNotification
		err := r.WithTx(ctx, func(q *db.Queries) error {
			locked, err := q.LockGlobalNotifications(ctx)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			current, err := q.GetGlobalNotifications(ctx)
			if err != nil {
				return err
			}
			update := params
			update.GotifyToken.Valid = params.GotifyToken.Valid &&
				params.GotifyToken.String != ""
			update.ClearGotifyToken = gotifyTokenClearFlag(
				params.ClearGotifyToken, current.GotifyUrl,
				params.GotifyUrl, params.GotifyToken,
			)
			settings, err = q.UpdateGlobalNotifications(ctx, update)
			return err
		})
		return settings, err
	})
}

func gotifyTokenClearFlag(
	clearFlag int64,
	current, destination, replacement sql.NullString,
) int64 {
	if !current.Valid {
		current.String = ""
	}
	if !destination.Valid {
		destination.String = ""
	}
	if clearFlag == 1 || ((!replacement.Valid || replacement.String == "") &&
		current.String != destination.String) {
		return 1
	}
	return clearFlag
}
