package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

// tstReleaseRouter builds the full server router like production.
func tstReleaseRouter(t *testing.T, h *harness) *chi.Mux {
	t.Helper()
	return server.NewRouter(
		h.repo,
		h.runner,
		cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
		),
		handler.NewAuthHandler(h.repo),
	)
}

func tstReleasePath(projectID, releaseID int64) string {
	return fmt.Sprintf(
		"/api/v1/projects/%d/releases/%d", projectID, releaseID,
	)
}

func tstReleaseGet(
	t *testing.T, r *chi.Mux, path, token string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func tstSeedVar(
	t *testing.T, h *harness, projectID int64,
	name, value string, secret bool,
) {
	t.Helper()
	var secretFlag int64
	if secret {
		secretFlag = 1
	}
	if _, err := h.repo.CreateVariable(
		context.Background(),
		db.CreateVariableParams{
			ProjectID: projectID,
			Name:      name,
			Value: sql.NullString{
				String: value,
				Valid:  value != "",
			},
			Secret: secretFlag,
		},
	); err != nil {
		t.Fatal(err)
	}
}

func tstAddMember(
	t *testing.T, h *harness, projectID, userID int64, role string,
) {
	t.Helper()
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
			Role:      role,
		},
	); err != nil {
		t.Fatal(err)
	}
}

// TestRelease_SecretMaskAcrossRoles guards issue #30: no ordinary
// GET /api/v1/projects/{id}/releases/{relId} response may contain a
// secret snapshot value in plaintext, for any role that can reach the
// endpoint. Non-secret snapshot values stay readable.
func TestRelease_SecretMaskAcrossRoles(t *testing.T) {
	h := newAPIHarness(t)
	enableMaskingSecretBox(t, h)
	r := tstReleaseRouter(t, h)
	admin := seedAPIUser(t, h.repo, "rel-admin@example.com", "admin")
	_, tokenAdmin := seedAPIToken(t, h.repo, admin.ID)
	p := seedProject(t, h.repo)
	tstAddMember(t, h, p.ID, admin.ID, "admin")

	const secretValue = "release-plaintext-secret"
	tstSeedVar(t, h, p.ID, "rel-secret", secretValue, true)
	const openValue = "plain-open-value"
	tstSeedVar(t, h, p.ID, "rel-open", openValue, false)

	// Snapshots are immutable and role-independent, so creating them
	// at the repository layer makes every role a pure reader — which
	// is what the issue asks to cover.
	release, err := handler.CreateReleaseSnapshot(
		context.Background(), h.repo, p.ID, "v1",
	)
	if err != nil {
		t.Fatal(err)
	}

	members := map[string]string{}
	for _, m := range []struct {
		user, userRole, memberRole string
	}{
		{"rel-dep@example.com", "deployer", "deployer"},
		// project_members.role only admits admin/deployer; the
		// viewer's read gate is membership, the user role is what
		// the CSRF layer checks on writes.
		{"rel-view@example.com", "viewer", "admin"},
	} {
		u := seedAPIUser(t, h.repo, m.user, m.userRole)
		tstAddMember(t, h, p.ID, u.ID, m.memberRole)
		_, full := seedAPIToken(t, h.repo, u.ID)
		members[m.userRole] = full
	}
	tokens := map[string]string{
		"admin":    tokenAdmin,
		"deployer": members["deployer"],
		"viewer":   members["viewer"],
	}
	for name, token := range tokens {
		name, token := name, token
		t.Run(name+" read is masked", func(t *testing.T) {
			rec := tstReleaseGet(
				t, r, tstReleasePath(p.ID, release.ID), token,
			)
			if rec.Code != http.StatusOK {
				t.Fatalf(
					"expected 200, got %d: %s", rec.Code, rec.Body,
				)
			}
			s := rec.Body.String()
			if strings.Contains(s, secretValue) {
				t.Fatalf("release response leaked secret: %s", s)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode release: %v", err)
			}
			vars, ok := body["variables"].([]any)
			if !ok || len(vars) != 2 {
				t.Fatalf("bad variables: %v", body["variables"])
			}
			seen := map[string]bool{}
			for _, item := range vars {
				vv, ok := item.(map[string]any)
				if !ok {
					t.Fatalf("bad variable type: %T", item)
				}
				vname, _ := vv["name"].(string)
				seen[vname] = true
				val, valOK := vv["value"].(string)
				if !valOK {
					t.Fatalf(
						"snapshot value is not a JSON string: %T",
						vv["value"],
					)
				}
				flag, _ := vv["secret"].(float64)
				switch vname {
				case "rel-secret":
					if val != "" {
						t.Fatalf(
							"secret snapshot not masked: %q", val,
						)
					}
					if flag != 1 {
						t.Fatal("secret flag lost")
					}
				case "rel-open":
					if val != openValue {
						t.Fatalf(
							"non-secret snapshot changed: %q", val,
						)
					}
					if flag != 0 {
						t.Fatal("open flag changed")
					}
				default:
					t.Fatalf("unexpected snapshot name: %q", vname)
				}
			}
			if !seen["rel-secret"] || !seen["rel-open"] {
				t.Fatalf(
					"seeded variables missing: %v", seen,
				)
			}
		})
	}
}

// TestRelease_SecretOnlySnapshotHasNoValue covers the edge cases: a
// secret snapshot emits the masked empty string (never null, never
// plaintext) and keeps its secret flag; a NULL-valued open snapshot
// stays a plain "" string.
func TestRelease_SecretOnlySnapshotHasNoValue(t *testing.T) {
	h := newAPIHarness(t)
	enableMaskingSecretBox(t, h)
	r := tstReleaseRouter(t, h)
	admin := seedAPIUser(t, h.repo, "rel-only@example.com", "admin")
	_, tokenAdmin := seedAPIToken(t, h.repo, admin.ID)
	p := seedProject(t, h.repo)
	tstAddMember(t, h, p.ID, admin.ID, "admin")

	tstSeedVar(t, h, p.ID, "only-secret", "only-secret-value", true)
	tstSeedVar(t, h, p.ID, "only-open", "", false)

	release, err := handler.CreateReleaseSnapshot(
		context.Background(), h.repo, p.ID, "v-only",
	)
	if err != nil {
		t.Fatal(err)
	}

	rec := tstReleaseGet(
		t, r, tstReleasePath(p.ID, release.ID), tokenAdmin,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if strings.Contains(body, "only-secret-value") {
		t.Fatalf("release response leaked secret: %s", body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	vars, ok := parsed["variables"].([]any)
	if !ok || len(vars) != 2 {
		t.Fatalf("bad variables: %v", parsed["variables"])
	}
	onlyOpened, onlySecreted := false, false
	for _, item := range vars {
		vv, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("bad variable type: %T", item)
		}
		name, _ := vv["name"].(string)
		val, valOK := vv["value"].(string)
		if !valOK || val != "" {
			t.Fatalf(
				"%q snapshot value must be the masked empty string: %q",
				name, vv["value"],
			)
		}
		switch name {
		case "only-secret":
			onlySecreted = true
			flag, _ := vv["secret"].(float64)
			if flag != 1 {
				t.Fatal("secret flag lost")
			}
		case "only-open":
			onlyOpened = true
			flag, _ := vv["secret"].(float64)
			if flag != 0 {
				t.Fatal("open flag changed")
			}
		default:
			t.Fatalf("unexpected snapshot name: %q", name)
		}
	}
	if !onlyOpened || !onlySecreted {
		t.Fatalf(
			"seeded snapshots missing or duplicated: open=%t"+
				" secret=%t", onlyOpened, onlySecreted,
		)
	}
}

// TestRelease_CrossProjectIDORConcealsSnapshot guards the IDOR leg of
// issue #30: a member of project A hitting a release that belongs to
// project B gets not-found and never the snapshot values.
func TestRelease_CrossProjectIDORConcealsSnapshot(t *testing.T) {
	h := newAPIHarness(t)
	enableMaskingSecretBox(t, h)
	r := tstReleaseRouter(t, h)
	const secretValue = "idor-release-secret"

	projects := map[string]int64{}
	tokenA := ""
	for _, member := range []string{"idor-a", "idor-b"} {
		owner := seedAPIUser(
			t, h.repo, fmt.Sprintf("%s@example.com", member), "deployer",
		)
		_, token := seedAPIToken(t, h.repo, owner.ID)
		proj, err := h.repo.Queries.CreateProject(
			context.Background(),
			db.CreateProjectParams{
				Name:        fmt.Sprintf("rel-idor-%s", member),
				Description: sql.NullString{},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		tstAddMember(t, h, proj.ID, owner.ID, "admin")
		projects[member] = proj.ID
		if member == "idor-a" {
			tokenA = token
		} else {
			tstSeedVar(t, h, proj.ID, "idor-secret", secretValue, true)
		}
	}

	bRelease, err := handler.CreateReleaseSnapshot(
		context.Background(), h.repo, projects["idor-b"], "v-abor",
	)
	if err != nil {
		t.Fatal(err)
	}

	rec := tstReleaseGet(
		t, r, tstReleasePath(projects["idor-a"], bRelease.ID), tokenA,
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf(
			"cross-project release read: want 404, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}
	if s := rec.Body.String(); strings.Contains(s, secretValue) {
		t.Fatalf("IDOR response leaked secret: %s", s)
	}
}
