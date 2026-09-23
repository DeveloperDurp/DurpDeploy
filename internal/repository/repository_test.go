package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestDeploymentLogIterationReleasesConnectionBeforeCallback(t *testing.T) {
	repo := newTestRepo(t)
	repo.DB.SetMaxOpenConns(1)
	ctx := context.Background()

	project, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "export-project",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "export-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "export-release",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "success",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if _, err := repo.Queries.CreateDeploymentLog(
		ctx,
		db.CreateDeploymentLogParams{
			DeploymentID: deployment.ID,
			Line:         "export log",
		},
	); err != nil {
		t.Fatalf("create deployment log: %v", err)
	}

	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	iterationDone := make(chan error, 1)
	go func() {
		iterationDone <- repo.ForEachDeploymentLogByDeploymentAsc(
			ctx,
			deployment.ID,
			func(db.DeploymentLog) error {
				close(callbackStarted)
				<-releaseCallback
				return nil
			},
		)
	}()
	<-callbackStarted

	queryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var count int
	if err := repo.DB.QueryRowContext(
		queryCtx,
		"SELECT COUNT(*) FROM projects",
	).Scan(&count); err != nil {
		t.Fatalf("query while callback is blocked: %v", err)
	}
	close(releaseCallback)
	if err := <-iterationDone; err != nil {
		t.Fatalf("iterate deployment logs: %v", err)
	}
}

func TestDeploymentLogIterationOrderAcrossBatches(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	project, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "batch-order-project",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "batch-order-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "batch-order-release",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "success",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	const total = 600
	for i := 0; i < total; i++ {
		log, err := repo.Queries.CreateDeploymentLog(
			ctx,
			db.CreateDeploymentLogParams{
				DeploymentID: deployment.ID,
				Line:         fmt.Sprintf("line-%d", i),
			},
		)
		if err != nil {
			t.Fatalf("create deployment log: %v", err)
		}
		if i%3 != 0 {
			continue
		}
		if i == 0 {
			if _, err := repo.DB.Exec(
				`INSERT INTO deployment_steps
					(deployment_id, step_index, name, script_body)
					VALUES (?, 0, 'step', 'echo')`,
				deployment.ID,
			); err != nil {
				t.Fatalf("create step: %v", err)
			}
			if _, err := repo.DB.Exec(
				`INSERT INTO deployment_step_attempts
					(deployment_id, step_index, attempt, wait_deadline)
					VALUES (?, 0, 1, 0)`,
				deployment.ID,
			); err != nil {
				t.Fatalf("create step attempt: %v", err)
			}
		}
		if err := repo.Queries.CreateDeploymentLogScope(
			ctx,
			db.CreateDeploymentLogScopeParams{
				LogID:        log.ID,
				DeploymentID: deployment.ID,
				StepIndex:    sql.NullInt64{Int64: 0, Valid: true},
				Attempt:      sql.NullInt64{Int64: 1, Valid: true},
				Sequence:     int64(i / 3),
			},
		); err != nil {
			t.Fatalf("create scope: %v", err)
		}
	}

	var got []int64
	err = repo.ForEachDeploymentLogByDeploymentAsc(
		ctx,
		deployment.ID,
		func(log db.DeploymentLog) error {
			got = append(got, log.ID)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("iterate deployment logs: %v", err)
	}
	if len(got) != total {
		t.Fatalf("iterated %d logs, want %d", len(got), total)
	}

	// Legacy ordering from the pre-batching single query; the batched
	// implementation must produce the exact same sequence.
	rows, err := repo.DB.Query(`
SELECT l.id
FROM deployment_logs l
LEFT JOIN deployment_log_scopes s ON s.log_id = l.id
WHERE l.deployment_id = ?
ORDER BY CASE
    WHEN s.step_index IS NULL AND s.attempt IS NULL THEN 0 ELSE 1
END,
CASE
    WHEN s.step_index IS NULL AND s.attempt IS NULL THEN s.sequence
END,
l.created_at ASC, l.id ASC`, deployment.ID)
	if err != nil {
		t.Fatalf("reference query: %v", err)
	}
	defer rows.Close()
	var want []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan reference: %v", err)
		}
		want = append(want, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reference rows: %v", err)
	}
	if len(want) != total {
		t.Fatalf("reference returned %d rows, want %d", len(want), total)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: got id %d, want id %d (batch boundary skip or reorder)", i, got[i], want[i])
		}
	}
}

func TestVariables_EncryptedAtRest(t *testing.T) {
	repo := newTestRepo(t)

	key := make([]byte, 32)
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	repo.SetSecretBox(box)

	ctx := context.Background()
	proj, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "secret-proj",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	const plaintext = "s3cr3t-token-value"
	created, err := repo.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: proj.ID,
		Name:      "API_TOKEN",
		Value:     sql.NullString{String: plaintext, Valid: true},
		Secret:    1,
	})
	if err != nil {
		t.Fatalf("CreateVariable: %v", err)
	}
	if created.Value.String != plaintext {
		t.Fatalf(
			"wrapper should return plaintext, got %q",
			created.Value.String,
		)
	}

	raw, err := repo.Queries.GetVariable(ctx, created.ID)
	if err != nil {
		t.Fatalf("raw GetVariable: %v", err)
	}
	if raw.Value.String == plaintext {
		t.Fatalf("raw DB row must not contain the plaintext value")
	}
	if strings.Contains(raw.Value.String, plaintext) {
		t.Fatalf("raw DB row must not contain the plaintext substring")
	}

	decrypted, err := repo.GetVariable(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetVariable: %v", err)
	}
	if decrypted.Value.String != plaintext {
		t.Fatalf("expected decrypted plaintext, got %q", decrypted.Value.String)
	}

	listed, err := repo.ListVariablesByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListVariablesByProject: %v", err)
	}
	if len(listed) != 1 || listed[0].Value.String != plaintext {
		t.Fatalf("ListVariablesByProject did not decrypt: %+v", listed)
	}
}

func TestVariables_NoSecretBoxIsPlaintextPassthrough(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	proj, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "plain-proj",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	created, err := repo.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: proj.ID,
		Name:      "PLAIN",
		Value:     sql.NullString{String: "plain-value", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateVariable: %v", err)
	}

	raw, err := repo.Queries.GetVariable(ctx, created.ID)
	if err != nil {
		t.Fatalf("raw GetVariable: %v", err)
	}
	if raw.Value.String != "plain-value" {
		t.Fatalf(
			"expected plaintext passthrough without a box, got %q",
			raw.Value.String,
		)
	}
}

func TestVariables_ListPaginatedDecryptsAndFilters(t *testing.T) {
	repo := newTestRepo(t)

	key := make([]byte, 32)
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	repo.SetSecretBox(box)

	ctx := context.Background()
	proj, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "paginated-secret-proj",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env, err := repo.Queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{
		Name: "paginated-env",
	})
	if err != nil {
		t.Fatalf("create env: %v", err)
	}

	for _, v := range []db.CreateVariableParams{
		{
			ProjectID: proj.ID,
			Name:      "PAG_SECRET",
			Value:     sql.NullString{String: "pag-secret-value", Valid: true},
			Secret:    1,
		},
		{
			ProjectID:     proj.ID,
			Name:          "PAG_PLAIN",
			Value:         sql.NullString{String: "pag-plain-value", Valid: true},
			EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
		},
	} {
		if _, err := repo.CreateVariable(ctx, v); err != nil {
			t.Fatalf("CreateVariable %s: %v", v.Name, err)
		}
	}

	base := db.ListVariablesByProjectPaginatedParams{
		ProjectID:  proj.ID,
		PageOffset: 0,
		PageLimit:  10,
	}
	list, err := repo.ListVariablesByProjectPaginated(ctx, base)
	if err != nil {
		t.Fatalf("ListVariablesByProjectPaginated: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 variables, got %d", len(list))
	}
	for _, v := range list {
		if v.Secret != 0 && v.Value.String == "pag-secret-value" {
			continue
		}
		if v.Secret == 0 && v.Value.String == "pag-plain-value" {
			continue
		}
		t.Fatalf(
			"list returned non-plaintext for %q: %q",
			v.Name,
			v.Value.String,
		)
	}

	// Secret-only filter keeps masked values out of the response while
	// the stored values still decrypt for the runner.
	secretOnly := base
	secretOnly.FSecretOnly = 1
	list, err = repo.ListVariablesByProjectPaginated(ctx, secretOnly)
	if err != nil {
		t.Fatalf("secret-only list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "PAG_SECRET" ||
		list[0].Value.String != "pag-secret-value" {
		t.Fatalf("secret-only list wrong: %+v", list)
	}

	envFiltered := base
	envFiltered.FEnvironmentID = env.ID
	list, err = repo.ListVariablesByProjectPaginated(ctx, envFiltered)
	if err != nil {
		t.Fatalf("env-filtered list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "PAG_PLAIN" {
		t.Fatalf("env-filtered list wrong: %+v", list)
	}

	// Unreadable ciphertext surfaces as an error, not silent garbage.
	if _, err := repo.Queries.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: proj.ID,
		Name:      "PAG_CORRUPT",
		Value:     sql.NullString{String: "not-a-ciphertext", Valid: true},
		Secret:    1,
	}); err != nil {
		t.Fatalf("CreateVariable PAG_CORRUPT: %v", err)
	}
	if _, err := repo.ListVariablesByProjectPaginated(ctx, base); err == nil {
		t.Fatal("expected decryption error for corrupt ciphertext")
	}

	repo.DB.Close()
	if _, err := repo.ListVariablesByProjectPaginated(
		ctx, base,
	); err == nil {
		t.Fatal("expected query error on closed connection")
	}
}

func TestRepository_WithTx_rollsBackAllWritesWhenCallbackFails(t *testing.T) {
	// Given: a migrated repository and a transaction callback that returns an error.
	repo := newTestRepo(t)
	ctx := context.Background()
	wantErr := errors.New("stop transaction")

	// When: the callback writes a user and then fails.
	err := repo.WithTx(ctx, func(queries *db.Queries) error {
		_, err := queries.CreateUser(ctx, db.CreateUserParams{
			Email:        "rollback@example.com",
			PasswordHash: "hash",
			Name:         "Rollback",
			Role:         "admin",
		})
		if err != nil {
			return err
		}
		return wantErr
	})

	// Then: the callback error survives and the write is not committed.
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTx error = %v, want %v", err, wantErr)
	}
	_, err = repo.Queries.GetUserByEmail(ctx, "rollback@example.com")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rolled back user lookup error = %v, want sql.ErrNoRows", err)
	}
}

func newTestRepo(t *testing.T) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run(
		"file:" + t.TempDir() + "/repository.db?_pragma=foreign_keys(1)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn)
}

func TestVariables_ListPaginated_ErrorPaths(t *testing.T) {
	repo := newTestRepo(t)

	// Query error: close the DB so the query fails.
	repo.DB.Close()
	_, err := repo.ListVariablesByProjectPaginated(
		context.Background(),
		db.ListVariablesByProjectPaginatedParams{},
	)
	if err == nil {
		t.Fatal("expected query error on closed DB")
	}
	_, err = repo.UpdateVariableKeepValue(
		context.Background(),
		db.UpdateVariableKeepValueParams{
			ID:     1,
			Name:   "n",
			Secret: 1,
		},
	)
	if err == nil {
		t.Fatal("expected query error on closed DB")
	}

	// Decrypt error: rotate the secret box so ciphertext
	// can no longer be decrypted.
	repo2 := newTestRepo(t)
	keyA := make([]byte, 32)
	boxA, err := secret.NewBox(keyA)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	repo2.SetSecretBox(boxA)
	ctx := context.Background()
	proj, err := repo2.Queries.CreateProject(
		ctx, db.CreateProjectParams{Name: "rot-proj"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	createdVar, err := repo2.CreateVariable(
		ctx, db.CreateVariableParams{
			ProjectID: proj.ID,
			Name:      "K",
			Value: sql.NullString{
				String: "v", Valid: true,
			},
			Secret: 1,
		},
	)
	if err != nil {
		t.Fatalf("CreateVariable: %v", err)
	}
	keyB := make([]byte, 32)
	keyB[0] = 1
	boxB, err := secret.NewBox(keyB)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	repo2.SetSecretBox(boxB)
	_, err = repo2.ListVariablesByProjectPaginated(
		ctx,
		db.ListVariablesByProjectPaginatedParams{
			ProjectID:  proj.ID,
			PageOffset: 0,
			PageLimit:  10,
		},
	)
	if err == nil {
		t.Fatal("expected decrypt error after key rotation")
	}
	_, err = repo2.UpdateVariableKeepValue(
		ctx,
		db.UpdateVariableKeepValueParams{
			ID:            createdVar.ID,
			Name:          "K2",
			EnvironmentID: sql.NullInt64{},
			Secret:        1,
		},
	)
	if err == nil {
		t.Fatal("expected decrypt error after key rotation")
	}

	// Update error: non-existent variable ID causes the
	// UPDATE to return sql.ErrNoRows, exercising the inner
	// error return inside the WithTx closure.
	repo3 := newTestRepo(t)
	_, err = repo3.UpdateVariableKeepValue(
		context.Background(),
		db.UpdateVariableKeepValueParams{
			ID:            999999,
			Name:          "ghost",
			EnvironmentID: sql.NullInt64{},
			Secret:        0,
		},
	)
	if err == nil {
		t.Fatal("expected error for non-existent variable")
	}
}
