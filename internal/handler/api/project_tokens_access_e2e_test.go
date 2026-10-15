//go:build e2e

package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type changingTokenDB struct {
	db.DBTX
	hash    string
	id      string
	changed atomic.Bool
	change  func()
}

func (d *changingTokenDB) QueryRowContext(
	ctx context.Context, query string, args ...any,
) *sql.Row {
	row := d.DBTX.QueryRowContext(ctx, query, args...)
	if strings.Contains(query, "FROM api_tokens") && len(args) > 0 &&
		(args[0] == d.hash || args[0] == d.id) &&
		d.changed.CompareAndSwap(false, true) {
		d.change()
	}
	return row
}

func TestProjectTokensConcurrentScopeAuditAPIE2E(t *testing.T) {
	for _, surface := range []string{"API", "web"} {
		t.Run(surface, func(t *testing.T) {
			f := newArtifactE2E(t)
			other := seedProject(t, f.h.repo)
			if _, err := f.h.repo.DB.ExecContext(t.Context(),
				"PRAGMA journal_mode = WAL"); err != nil {
				t.Fatal(err)
			}
			token := mintProjectToken(t, f, "viewer", f.project.ID)
			changing := &changingTokenDB{
				DBTX: f.h.repo.DB, id: token.TokenID,
				change: func() {
					_, err := f.h.repo.ReplaceTokenScope(
						t.Context(),
						token.TokenID,
						repository.TokenScope{Scope: "scoped", Role: "admin",
							ProjectIDs: []int64{other.ID}},
					)
					if err != nil {
						t.Errorf("overlapping owner edit: %v", err)
					}
				},
			}
			f.h.repo.Queries = db.New(changing)
			if surface == "API" {
				f.api(t, "PATCH", "/api/v1/tokens/"+token.TokenID,
					map[string]any{"scope": "scoped", "role": "deployer",
						"project_ids": []int64{f.project.ID}}, 200)
			} else {
				f.web(t, "POST", "/settings/tokens/"+token.TokenID+"/scope",
					url.Values{"scope": {"scoped"}, "role": {"deployer"},
						"project_ids": {fmt.Sprint(f.project.ID)}}, 303)
			}
			if !changing.changed.Load() {
				t.Fatal("owner edits did not overlap")
			}
			var details string
			if err := f.h.repo.DB.QueryRowContext(
				t.Context(),
				"SELECT details FROM audit_log WHERE action = 'update_api_token_scope' ORDER BY id DESC LIMIT 1",
			).Scan(&details); err != nil {
				t.Fatal(err)
			}
			var change struct {
				Previous repository.TokenScope `json:"previous_scope"`
			}
			if err := json.Unmarshal([]byte(details), &change); err != nil {
				t.Fatal(err)
			}
			if change.Previous.Role != "admin" ||
				len(change.Previous.ProjectIDs) != 1 ||
				change.Previous.ProjectIDs[0] != other.ID {
				t.Fatal("scope audit did not capture the replaced grants")
			}
		})
	}
}

func TestProjectTokensConcurrentScopeEditAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	other := seedProject(t, f.h.repo)
	// WAL permits the owner edit while the initial token read is open.
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"PRAGMA journal_mode = WAL"); err != nil {
		t.Fatal(err)
	}
	token := mintProjectToken(t, f, "admin", f.project.ID)
	changing := &changingTokenDB{
		DBTX: f.h.repo.DB, hash: auth.HashApiToken(token.ID),
		change: func() {
			err := f.h.repo.WithTx(t.Context(), func(q *db.Queries) error {
				return repository.SaveTokenScope(t.Context(), q, token.TokenID,
					repository.TokenScope{Scope: "scoped", Role: "viewer",
						ProjectIDs: []int64{other.ID}})
			})
			if err != nil {
				t.Errorf("concurrent scope edit: %v", err)
			}
		},
	}
	f.h.repo.Queries = db.New(changing)
	f.token = token.ID
	// Neither admin/A nor viewer/B permits a write to B.
	f.api(t, "POST", fmt.Sprintf("/api/v1/projects/%d/steps", other.ID),
		map[string]string{"name": "forbidden", "script_body": "echo forbidden"},
		403)
	if !changing.changed.Load() {
		t.Fatal("scope edit did not overlap authentication")
	}
	f.api(t, "GET", f.base(), nil, 403)
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", other.ID), nil, 200)
}

func TestProjectTokensOwnerAccessAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	admin := f.token
	other := seedProject(t, f.h.repo)
	user := seedAPIUser(t, f.h.repo, "token-owner@example.test", "deployer")
	_, global := seedAPIToken(t, f.h.repo, user.ID)
	f.api(t, "POST", f.base()+"/members", map[string]any{
		"user_id": user.ID, "role": "admin",
	}, 201)
	f.token = global
	f.api(t, "POST", "/api/v1/tokens", map[string]any{
		"name": "inaccessible", "scope": "scoped", "role": "admin",
		"project_ids": []int64{other.ID},
	}, 422)
	token := mintProjectToken(t, f, "admin", f.project.ID)
	f.token = token.ID
	f.api(t, "GET", f.base(), nil, 200)
	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(t, f.h.repo, release.ID, f.environment.ID,
		"pending_approval")
	f.api(t, "POST", fmt.Sprintf("/api/v1/deployments/%d/approve",
		deployment.ID), nil, 403)
	f.token = admin
	f.api(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/admin/users/%d", user.ID),
		map[string]string{
			"email": user.Email,
			"name":  user.Name,
			"role":  "viewer",
		},
		200,
	)
	f.token = token.ID
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "forbidden", "script_body": "echo forbidden",
	}, 403)
	f.api(t, "GET", f.base(), nil, 200)
	f.token = admin
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("%s/members/%d", f.base(), user.ID),
		nil,
		204,
	)
	f.token = token.ID
	f.api(t, "GET", f.base(), nil, 403)
	var listed struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(f.api(t, "GET", "/api/v1/projects", nil, 200),
		&listed); err != nil {
		t.Fatal(err)
	}
	if listed.Total != 0 {
		t.Fatal("removed owner access remains in project count")
	}
}

func TestProjectTokensSharedResourcesAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	owner := f.token
	var lc struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		"/api/v1/lifecycles",
		map[string]string{
			"name": "selected lifecycle",
		},
		201,
	), &lc); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/lifecycles/%d/stages", lc.ID),
		map[string]any{
			"environment_id": f.environment.ID,
			"sort_order":     0,
		},
		201,
	)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lc.ID,
	}, 200)
	otherEnv := seedEnv(t, f.h.repo)
	var otherLC struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		"/api/v1/lifecycles",
		map[string]string{
			"name": "unrelated lifecycle",
		},
		201,
	), &otherLC); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"viewer", "deployer", "admin"} {
		f.token = owner
		token := mintProjectToken(t, f, role, f.project.ID)
		f.token = token.ID
		for _, path := range []string{"environments", "lifecycles"} {
			var listed struct {
				Items []json.RawMessage `json:"items"`
				Total int               `json:"total"`
			}
			if err := json.Unmarshal(f.api(t, "GET", "/api/v1/"+path, nil, 200),
				&listed); err != nil {
				t.Fatal(err)
			}
			if listed.Total != 1 || len(listed.Items) != 1 {
				t.Fatalf(
					"%s exposed shared resources outside the selected project",
					path,
				)
			}
		}
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/lifecycles/%d", otherLC.ID),
			nil,
			404,
		)
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/environments/%d", otherEnv.ID),
			nil,
			404,
		)
		env := f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
			nil,
			200,
		)
		if strings.Contains(string(env), `"verification_target"`) {
			t.Fatal("scoped token exposed instance verification configuration")
		}
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/lifecycles/%d/variables", lc.ID),
			nil,
			403,
		)
		f.api(t, "GET", f.base()+"/templates-picker", nil, 403)
		f.api(t, "PUT", f.base(), map[string]any{
			"name": f.project.Name, "lifecycle_id": otherLC.ID,
		}, 403)
		f.api(
			t,
			"POST",
			"/api/v1/environments",
			map[string]string{"name": "escape"},
			403,
		)
	}
}

func TestProjectTokensScopeAuditAndRevocationAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	owner := f.token
	other := seedProject(t, f.h.repo)
	token := mintProjectToken(t, f, "viewer", f.project.ID)
	grant := map[string]any{"scope": "scoped", "role": "deployer",
		"project_ids": []int64{other.ID}}
	f.api(t, "PATCH", "/api/v1/tokens/"+token.TokenID, grant, 200)
	var details string
	if err := f.h.repo.DB.QueryRowContext(
		t.Context(),
		"SELECT details FROM audit_log WHERE action = 'update_api_token_scope' ORDER BY id DESC LIMIT 1",
	).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var change struct {
		TokenID  string `json:"token_id"`
		Previous struct {
			Role string `json:"role"`
		} `json:"previous_scope"`
		ProjectIDs []int64 `json:"project_ids"`
	}
	if err := json.Unmarshal([]byte(details), &change); err != nil {
		t.Fatal(err)
	}
	if change.TokenID != token.TokenID || change.Previous.Role != "viewer" ||
		len(change.ProjectIDs) != 1 || change.ProjectIDs[0] != other.ID ||
		strings.Contains(details, token.ID) {
		t.Fatal("scope audit missing non-secret before/after grants")
	}
	f.token = token.ID
	f.api(t, "GET", f.base(), nil, 403)
	path := fmt.Sprintf("/api/v1/projects/%d", other.ID)
	f.api(t, "POST", path+"/variables", map[string]any{
		"name":   "secret",
		"value":  "do-not-expose-scoped-secret",
		"secret": true,
	}, 201)
	body := f.api(t, "GET", path+"/variables", nil, 200)
	if strings.Contains(string(body), "do-not-expose-scoped-secret") {
		t.Fatal("secret exposed")
	}
	if err := f.h.repo.DB.QueryRowContext(
		t.Context(),
		"SELECT details FROM audit_log WHERE action = 'create_variable' ORDER BY id DESC LIMIT 1",
	).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var actor struct {
		TokenID   string `json:"api_token_id"`
		ProjectID int64  `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(details), &actor); err != nil {
		t.Fatal(err)
	}
	if actor.TokenID != token.TokenID || actor.ProjectID != other.ID {
		t.Fatal("scoped operation lost token or project attribution")
	}
	f.token = owner
	f.api(t, "DELETE", "/api/v1/tokens/"+token.TokenID, nil, 204)
	f.token = token.ID
	f.api(t, "GET", path, nil, 401)
	f.token = owner
	expired := mintProjectToken(t, f, "viewer", other.ID)
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"UPDATE api_tokens SET expires_at = ? WHERE id = ?",
		time.Now().Add(-time.Minute).Unix(), expired.TokenID); err != nil {
		t.Fatal(err)
	}
	f.token = expired.ID
	f.api(t, "GET", path, nil, 401)
	f.token = owner
	empty := mintProjectToken(t, f, "viewer", other.ID)
	if err := f.h.repo.Queries.DeleteApiTokenProjects(
		t.Context(),
		empty.TokenID,
	); err != nil {
		t.Fatal(err)
	}
	f.token = empty.ID
	f.api(t, "GET", "/api/v1/projects", nil, 401)
}

func TestProjectTokensBusyScopeEditAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	token := mintProjectToken(t, f, "viewer", f.project.ID)
	tx, err := f.h.repo.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(),
		"UPDATE api_tokens SET last_used_at=last_used_at WHERE id=?",
		token.TokenID); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		released <- tx.Rollback()
	}()
	f.api(t, "PATCH", "/api/v1/tokens/"+token.TokenID,
		map[string]any{"scope": "scoped", "role": "deployer",
			"project_ids": []int64{f.project.ID}}, 200)
	if err := <-released; err != nil {
		t.Fatal(err)
	}
}
