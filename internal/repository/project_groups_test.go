package repository

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectGroupsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, engine string) {
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, engine),
		)
		group := assertProjectGroupNames(t, repo)
		projects := seedProjectGroupCatalog(t, repo)
		assertProjectGroupCatalog(t, repo, group, projects)
		assertProjectGroupBoundaries(t, repo)
		assertProjectGroupCreation(t, repo)
	})
}

func assertProjectGroupCreation(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := t.Context()
	group, err := repo.SaveProjectGroup(ctx, 0, "Creation group")
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email: "group-creator@test.local", Name: "Creator",
		PasswordHash: "unused", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	access := ProjectCatalogAccess{IsAdmin: 1, UserID: user.ID}
	created, err := repo.CreateProject(ctx,
		db.CreateProjectParams{Name: "Grouped creation"}, access, &group.ID)
	if err != nil || !created.GroupID.Valid ||
		created.GroupID.Int64 != group.ID {
		t.Fatalf("grouped creation: %+v %v", created, err)
	}
	membership, err := repo.Queries.GetProjectMember(ctx,
		db.GetProjectMemberParams{ProjectID: created.ID, UserID: user.ID})
	if err != nil || membership.Role != "admin" {
		t.Fatalf("creator membership: %+v %v", membership, err)
	}
	before, err := repo.Queries.CountProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	access.UserID = 999999
	if _, err := repo.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name: "Rejected creation",
		},
		access,
		&group.ID,
	); err == nil {
		t.Fatal("missing creator accepted")
	}
	after, err := repo.Queries.CountProjects(ctx)
	if err != nil || after != before {
		t.Fatalf(
			"failed membership left a project: count=%d want=%d %v",
			after,
			before,
			err,
		)
	}
}

func assertProjectGroupNames(t *testing.T, repo *Repository) db.ProjectGroup {
	ctx := t.Context()
	group, err := repo.SaveProjectGroup(ctx, 0, " Team ")
	if err != nil || group.Name != "Team" {
		t.Fatalf("create: %+v %v", group, err)
	}
	if _, err := repo.SaveProjectGroup(ctx, 0, "team"); err == nil {
		t.Fatal("duplicate group accepted")
	}
	for _, name := range []string{"", " ungrouped ", "bad\nname", strings.Repeat("a", 101)} {
		if _, err := repo.SaveProjectGroup(ctx, 0, name); !errors.Is(
			err,
			ErrProjectGroupName,
		) {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	return group
}

func seedProjectGroupCatalog(t *testing.T, repo *Repository) []db.Project {
	ctx := t.Context()
	var projects []db.Project
	for _, name := range []string{"group-check-C", "group-check-A", "group-check-B%_[!"} {
		p, err := repo.Queries.CreateProject(
			ctx,
			db.CreateProjectParams{
				Name: name,
				Description: sql.NullString{
					String: "catalog marker",
					Valid:  true,
				},
			},
		)
		if err != nil || p.GroupID.Valid {
			t.Fatalf("legacy project: %+v %v", p, err)
		}
		projects = append(projects, p)
	}
	return projects
}

func assertProjectGroupCatalog(
	t *testing.T,
	repo *Repository,
	group db.ProjectGroup,
	projects []db.Project,
) {
	ctx := t.Context()
	access := ProjectCatalogAccess{IsAdmin: 1}
	for _, p := range projects[:2] {
		if _, err := repo.MoveProjectGroup(
			ctx,
			access,
			p.ID,
			&group.ID,
		); err != nil {
			t.Fatal(err)
		}
	}
	options := ProjectCatalogOptions{
		Query:   "group-check",
		Sort:    "name",
		GroupID: -1,
		Limit:   1,
		Offset:  1,
	}
	rows, total, err := repo.ProjectCatalog(ctx, access, options)
	if err != nil || total != 3 || len(rows) != 1 ||
		rows[0].ID != projects[2].ID {
		t.Fatalf("page: %+v total=%d %v", rows, total, err)
	}
	options.Query, options.Offset = "%_[!", 0
	rows, total, err = repo.ProjectCatalog(ctx, access, options)
	if err != nil || total != 1 || len(rows) != 1 ||
		rows[0].ID != projects[2].ID {
		t.Fatalf("literal search: %+v %d %v", rows, total, err)
	}
	options.Query, options.Sort, options.GroupID = "catalog marker", "name_desc", group.ID
	rows, total, err = repo.ProjectCatalog(ctx, access, options)
	if err != nil || total != 2 || len(rows) != 1 ||
		rows[0].ID != projects[0].ID {
		t.Fatalf("group sort: %+v %d %v", rows, total, err)
	}
	assertProjectGroupDeletion(t, repo, group, projects)
}

func assertProjectGroupBoundaries(t *testing.T, repo *Repository) {
	ctx := t.Context()
	access := ProjectCatalogAccess{IsAdmin: 1}
	unicodeProject, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name: "Örion",
			Description: sql.NullString{
				String: "Äccent description",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"Örion", "Äccent description"} {
		rows, total, err := repo.ProjectCatalog(
			ctx,
			access,
			ProjectCatalogOptions{
				Query:   query,
				Sort:    "name",
				GroupID: -1,
				Limit:   100,
			},
		)
		if err != nil || total != 1 || len(rows) != 1 ||
			rows[0].ID != unicodeProject.ID {
			t.Fatalf(
				"exact Unicode search %q: %+v total=%d error=%v",
				query,
				rows,
				total,
				err,
			)
		}
	}
	for _, groupID := range []int64{1 << 31, 1<<63 - 1} {
		rows, total, err := repo.ProjectCatalog(
			ctx,
			access,
			ProjectCatalogOptions{
				Sort:    "newest",
				GroupID: groupID,
				Limit:   100,
			},
		)
		if err != nil || total != 0 || len(rows) != 0 {
			t.Fatalf(
				"int64 group filter %d: %+v total=%d error=%v",
				groupID,
				rows,
				total,
				err,
			)
		}
	}
}

func assertProjectGroupDeletion(
	t *testing.T,
	repo *Repository,
	group db.ProjectGroup,
	projects []db.Project,
) {
	ctx := t.Context()
	access := ProjectCatalogAccess{IsAdmin: 1}
	groups, err := repo.Queries.ListAccessibleProjectGroups(ctx, access)
	if err != nil || len(groups) != 1 || groups[0].ProjectCount != 2 {
		t.Fatalf("counts: %+v %v", groups, err)
	}
	if _, err := repo.SaveProjectGroup(ctx, group.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.DeleteProjectGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		retained, err := repo.Queries.GetProject(ctx, p.ID)
		if err != nil || retained.GroupID.Valid {
			t.Fatalf("group deletion lost project: %+v %v", retained, err)
		}
	}
}
