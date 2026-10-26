package repository

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

type panelQueryRecorder struct {
	db.DBTX
	query          string
	args           []any
	reads, maxArgs int
}

func (r *panelQueryRecorder) QueryContext(
	ctx context.Context, query string, args ...any,
) (*sql.Rows, error) {
	r.query, r.args = query, append([]any(nil), args...)
	r.reads++
	r.maxArgs = max(r.maxArgs, len(args))
	return r.DBTX.QueryContext(ctx, query, args...)
}

func TestProjectPanelBatchEmpty(t *testing.T) {
	for _, ids := range [][2][]int64{{nil, {1}}, {{1}, nil}, {nil, nil}} {
		rows, err := (&Repository{}).ProjectPanelDeployments(
			t.Context(),
			ids[0],
			ids[1],
		)
		if err != nil || len(rows) != 0 {
			t.Fatalf("empty scope: rows=%v error=%v", rows, err)
		}
	}
}

func checkProjectPanelBatch(
	t *testing.T, repo *Repository, engine string, otherProject, otherEnv int64,
) {
	t.Helper()
	queries := repo.Queries
	recorder := &panelQueryRecorder{DBTX: repo.DB}
	repo.Queries = db.New(recorder)
	t.Cleanup(func() { repo.Queries = queries })
	projects, envs := []int64{
		1,
		otherProject,
		otherProject + 1,
	}, []int64{
		1,
		otherEnv,
		otherEnv + 1,
	}
	rows, err := repo.ProjectPanelDeployments(t.Context(), projects, envs)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.reads != 1 {
		t.Fatalf("batch reads=%d want 1", recorder.reads)
	}
	checkProjectPanelPlan(t, repo.DB, engine, recorder.query, recorder.args)
	grouped := make(map[[2]int64][]db.Deployment)
	for _, row := range rows {
		release, err := queries.GetRelease(
			t.Context(),
			row.Deployment.ReleaseID,
		)
		if err != nil || release.ProjectID != row.ProjectID ||
			release.Version != row.Version {
			t.Fatalf("batch release mismatch: %+v, %+v, %v", row, release, err)
		}
		key := [2]int64{row.ProjectID, row.Deployment.EnvironmentID}
		grouped[key] = append(grouped[key], row.Deployment)
	}
	for _, project := range projects {
		for _, env := range envs {
			checkProjectPanelLegacyScope(t, queries, project, env, grouped)
		}
	}
	// Cross both bind boundaries. Only project 1/environment 1 exist in this
	// scope, and their six rows must appear once across the four queries.
	projects, envs = []int64{1}, []int64{1}
	for i := int64(10_000); len(projects) < 501; i++ {
		projects, envs = append(projects, i), append(envs, i)
	}
	recorder.reads, recorder.maxArgs = 0, 0
	chunked, err := repo.ProjectPanelDeployments(t.Context(), projects, envs)
	if err != nil || len(chunked) != 6 || recorder.reads != 4 ||
		recorder.maxArgs > 1000 {
		t.Fatalf("chunked rows=%d reads=%d maxArgs=%d error=%v",
			len(chunked), recorder.reads, recorder.maxArgs, err)
	}
	for i, row := range chunked {
		if row.ProjectID != 1 ||
			!reflect.DeepEqual(row.Deployment, grouped[[2]int64{1, 1}][i]) {
			t.Fatalf("chunked scope/order: %+v", row)
		}
	}
}

func checkProjectPanelLegacyScope(
	t *testing.T,
	queries *db.Queries,
	project, env int64,
	grouped map[[2]int64][]db.Deployment,
) {
	t.Helper()
	recent, err := queries.ListRecentDeploymentsForProjectEnv(
		t.Context(),
		db.ListRecentDeploymentsForProjectEnvParams{
			ProjectID: project, EnvironmentID: env, Limit: 5,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := queries.GetLatestSuccessfulDeploymentForProjectEnv(
		t.Context(),
		db.GetLatestSuccessfulDeploymentForProjectEnvParams{
			ProjectID: project, EnvironmentID: env,
		},
	)
	if err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	found := false
	for _, row := range recent {
		found = found || row.ID == latest.ID
	}
	if latest.ID != 0 && !found {
		recent = append(recent, latest)
	}
	got := grouped[[2]int64{project, env}]
	if len(got) != len(recent) ||
		len(got) > 0 && !reflect.DeepEqual(got, recent) {
		t.Fatalf(
			"scope %d/%d: batch=%+v legacy=%+v",
			project,
			env,
			got,
			recent,
		)
	}
}

func checkProjectPanelPlan(
	t *testing.T, database *sql.DB, engine, query string, args []any,
) {
	t.Helper()
	conn, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	switch engine {
	case "SQLite":
		query = "EXPLAIN QUERY PLAN " + query
	case "PostgreSQL":
		query = "EXPLAIN (FORMAT TEXT) " + query
	case "SQLServer":
		// SHOWPLAN must compile a SQL batch rather than an sp_executesql RPC.
		// The captured arguments contain only fixture IDs.
		for _, arg := range args {
			query = strings.Replace(query, "?", fmt.Sprint(arg.(int64)), 1)
		}
		args = nil
		if _, err := conn.ExecContext(t.Context(), "SET SHOWPLAN_XML ON"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(
				context.Background(),
				5*time.Second,
			)
			defer cancel()
			if _, err := conn.ExecContext(ctx, "SET SHOWPLAN_XML OFF"); err != nil {
				t.Error(err)
			}
		}()
	}
	rows, err := conn.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	count := 0
	for {
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := readProjectPanelPlanRow(t, rows, columns)
			t.Logf("%s project panel plan: %v", engine, values)
			count++
		}
		if !rows.NextResultSet() {
			break
		}
	}
	if err := rows.Err(); err != nil || count == 0 {
		t.Fatalf("query plan rows=%d error=%v", count, err)
	}
}

func readProjectPanelPlanRow(
	t *testing.T, rows *sql.Rows, columns []string,
) []any {
	t.Helper()
	values, pointers := make(
		[]any,
		len(columns),
	), make(
		[]any,
		len(columns),
	)
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	for i, value := range values {
		if data, ok := value.([]byte); ok {
			values[i] = string(data)
		}
	}
	return values
}
