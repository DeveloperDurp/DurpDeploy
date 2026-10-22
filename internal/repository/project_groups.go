package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"durpdeploy/internal/db"
)

var ErrProjectGroupName = errors.New(
	"group name must contain 1 to 100 characters, without control characters, and cannot be Ungrouped",
)

// ProjectCatalogAccess uses the owner's role and the token's project selection.
// A token ceiling restricts writes separately and never expands these reads.
type ProjectCatalogAccess = db.ListAccessibleProjectGroupsParams

type ProjectCatalogOptions struct {
	Query, Sort            string
	GroupID, Limit, Offset int64
}

func (r *Repository) ProjectCatalog(
	ctx context.Context,
	access ProjectCatalogAccess,
	options ProjectCatalogOptions,
) ([]db.Project, int64, error) {
	search := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_", "[", "![").
		Replace(options.Query)
	filter := db.CountProjectCatalogParams{
		IsAdmin: access.IsAdmin, UserID: access.UserID, TokenID: access.TokenID,
		GroupFilter: options.GroupID, Search: "%" + search + "%",
	}
	total, err := r.Queries.CountProjectCatalog(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	projects, err := r.Queries.ListProjectCatalog(
		ctx,
		db.ListProjectCatalogParams{
			IsAdmin: filter.IsAdmin, UserID: filter.UserID, TokenID: filter.TokenID,
			GroupFilter: filter.GroupFilter, Search: filter.Search,
			SortOrder: options.Sort, PageLimit: options.Limit, PageOffset: options.Offset,
		},
	)
	return projects, total, err
}

func (r *Repository) SaveProjectGroup(
	ctx context.Context,
	id int64,
	name string,
) (db.ProjectGroup, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" ||
		utf8.RuneCountInString(name) > 100 ||
		strings.EqualFold(name, "Ungrouped") ||
		strings.ContainsFunc(name, unicode.IsControl) {
		return db.ProjectGroup{}, ErrProjectGroupName
	}
	key := strings.ToLower(name)
	if id == 0 {
		return r.Queries.CreateProjectGroup(
			ctx,
			db.CreateProjectGroupParams{Name: name, NameKey: key},
		)
	}
	return r.Queries.UpdateProjectGroup(
		ctx,
		db.UpdateProjectGroupParams{ID: id, Name: name, NameKey: key},
	)
}

// MoveProjectGroup checks destination visibility in the same transaction as the move.
// Project middleware enforces the source project's authoring permission.
func (r *Repository) MoveProjectGroup(
	ctx context.Context,
	access ProjectCatalogAccess,
	projectID int64,
	groupID *int64,
) (project db.Project, err error) {
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			var group sql.NullInt64
			if groupID != nil {
				if *groupID <= 0 {
					return sql.ErrNoRows
				}
				groups, err := q.ListAccessibleProjectGroups(ctx, access)
				if err != nil {
					return err
				}
				if !slices.ContainsFunc(
					groups,
					func(candidate db.ListAccessibleProjectGroupsRow) bool {
						return candidate.ID == *groupID
					},
				) {
					return sql.ErrNoRows
				}
				group = sql.NullInt64{Int64: *groupID, Valid: true}
			}
			var moveErr error
			project, moveErr = q.SetProjectGroup(
				ctx,
				db.SetProjectGroupParams{ID: projectID, GroupID: group},
			)
			return moveErr
		})
	})
	return project, err
}
