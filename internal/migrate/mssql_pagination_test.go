package migrate

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
)

func verifyMSSQLProjectPagination(
	t *testing.T,
	ctx context.Context,
	queries *db.Queries,
) {
	t.Helper()
	for _, name := range []string{"mssql-page-two", "mssql-page-three"} {
		_, err := queries.CreateProject(ctx, db.CreateProjectParams{Name: name})
		requireNoError(t, err, "create paged project "+name)
	}
	firstPage, err := queries.ListProjectsPaginated(
		ctx,
		db.ListProjectsPaginatedParams{Limit: 2, Offset: 0},
	)
	requireNoError(t, err, "list first project page")
	secondPage, err := queries.ListProjectsPaginated(
		ctx,
		db.ListProjectsPaginatedParams{Limit: 2, Offset: 2},
	)
	requireNoError(t, err, "list second project page")
	if len(firstPage) != 2 || len(secondPage) != 1 ||
		firstPage[0].CreatedAt < firstPage[1].CreatedAt {
		t.Fatalf("project pages = %#v, %#v", firstPage, secondPage)
	}
	pageIDs := map[int64]bool{}
	for _, page := range [][]db.Project{firstPage, secondPage} {
		for _, item := range page {
			if pageIDs[item.ID] {
				t.Fatalf("project %d appeared in multiple pages", item.ID)
			}
			pageIDs[item.ID] = true
		}
	}
	if len(pageIDs) != 3 {
		t.Fatalf("paged project IDs = %#v, want 3 distinct rows", pageIDs)
	}
}
