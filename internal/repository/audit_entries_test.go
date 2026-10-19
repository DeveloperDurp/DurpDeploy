package repository

import (
	"database/sql"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestAuditIdentitiesAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		actor, err := repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
			Email: "operator@audit.test", Name: "Operator Élodie 100%", Role: "admin",
			PasswordHash: "not-an-authentication-fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		checkAuditIdentityFilters(t, repo, actor)
		checkAuditIdentityTargets(t, repo, actor.ID)
	})
}

func checkAuditIdentityFilters(t *testing.T, repo *Repository, actor db.User) {
	t.Helper()
	for range 2 {
		if _, err := repo.Queries.CreateAuditLog(t.Context(), db.CreateAuditLogParams{
			UserID: sql.NullInt64{Int64: actor.ID, Valid: true},
			Action: "update_user", EntityType: "user",
			EntityID: sql.NullInt64{Int64: actor.ID, Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"OPERATOR@AUDIT.TEST", actor.Name} {
		rows, err := repo.ListAuditLogEntries(
			t.Context(),
			db.ListAuditLogEntriesParams{
				FUser: sql.NullString{String: user, Valid: true}, PageLimit: 2,
			},
		)
		if err != nil || len(rows) != 2 {
			t.Fatalf("identity filter rows=%v error=%v", rows, err)
		}
		if rows[0].ID <= rows[1].ID || rows[0].ActorName != actor.Name ||
			rows[0].ActorEmail != actor.Email || rows[0].UserID.Int64 != actor.ID {
			t.Fatalf("identity, stable ID, or order lost: %+v", rows)
		}
	}
	rows, err := repo.ListAuditLogEntries(
		t.Context(),
		db.ListAuditLogEntriesParams{
			FUser: sql.NullString{String: "%", Valid: true}, PageLimit: 2,
		},
	)
	if err != nil || len(rows) != 0 {
		t.Fatalf("literal identity filter rows=%v error=%v", rows, err)
	}
}

func checkAuditIdentityTargets(t *testing.T, repo *Repository, actorID int64) {
	t.Helper()
	lifecycle, err := repo.Queries.CreateLifecycle(t.Context(),
		db.CreateLifecycleParams{Name: "audit lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	book, err := repo.Queries.CreateRunbook(t.Context(), db.CreateRunbookParams{
		ProjectID: 1, Name: "audit book",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment := createLegacyRemoteDeployment(t, repo)
	template, err := repo.Queries.CreateStepTemplate(
		t.Context(),
		db.CreateStepTemplateParams{
			Name:       "audit template",
			ScriptBody: "echo audit",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		kind string
		id   int64
		url  string
	}{
		{"user", actorID, fmt.Sprintf("/admin/users/%d/edit", actorID)},
		{"project", 1, "/projects/1"},
		{"environment", 1, "/environments/1/edit"},
		{"lifecycle", lifecycle.ID, fmt.Sprintf("/lifecycles/%d", lifecycle.ID)},
		{"template", template.ID, fmt.Sprintf("/templates/%d/edit", template.ID)},
		{"deployment", deployment.ID, fmt.Sprintf("/deployments/%d", deployment.ID)},
		{"release", 1, "/projects/1/releases/1"},
		{"runbook", book.ID, fmt.Sprintf("/projects/1/runbooks/%d", book.ID)},
		{"user", 999999, ""},
		{"unknown", 1, ""},
	} {
		entry, err := repo.Queries.CreateAuditLog(
			t.Context(),
			db.CreateAuditLogParams{
				Action: "target_check", EntityType: target.kind,
				EntityID: sql.NullInt64{Int64: target.id, Valid: true},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := repo.ListAuditLogEntries(
			t.Context(),
			db.ListAuditLogEntriesParams{
				FAction: sql.NullString{
					String: "target_check",
					Valid:  true,
				}, PageLimit: 1,
			},
		)
		if err != nil || len(rows) != 1 {
			t.Fatalf("target rows=%v error=%v", rows, err)
		}
		if rows[0].ID != entry.ID || rows[0].TargetURL != target.url ||
			rows[0].EntityID != entry.EntityID || rows[0].UserID.Valid {
			t.Fatalf("target %s: %+v", target.kind, rows[0])
		}
	}
	if err := repo.Queries.MarkRunbookDeleted(t.Context(),
		db.MarkRunbookDeletedParams{ID: book.ID, ProjectID: 1}); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListAuditLogEntries(
		t.Context(),
		db.ListAuditLogEntriesParams{
			FEntityType: sql.NullString{
				String: "runbook",
				Valid:  true,
			}, PageLimit: 1,
		},
	)
	if err != nil || len(rows) != 1 || rows[0].TargetURL != "" {
		t.Fatalf("deleted runbook target rows=%v error=%v", rows, err)
	}
}
