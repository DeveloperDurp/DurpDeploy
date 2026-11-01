//go:build e2e

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestCredentialEnrollmentAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Credential enrollment lifecycle"},
			201,
		),
		&lifecycle,
	)
	sharedBase := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle.ID)
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycle.ID},
		200,
	)
	for _, base := range []string{f.base(), sharedBase} {
		var ordinary struct {
			ID           int64
			IsCredential int64 `json:"is_credential"`
		}
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				base+"/variables",
				map[string]any{
					"name":   "ORDINARY_SECRET",
					"value":  "private-enrollment-value",
					"secret": true,
				},
				201,
			),
			&ordinary,
		)
		require.Zero(t, ordinary.IsCredential)
		var rows []repository.Credential
		decodeLifecycleTest(
			t,
			f.api(t, "GET", base+"/credentials", nil, 200),
			&rows,
		)
		require.Empty(t, rows)
		body := f.api(t, "GET", base+"/credentials/candidates", nil, 200)
		require.NotContains(t, string(body), "private-enrollment-value")
		decodeLifecycleTest(t, body, &rows)
		require.Len(t, rows, 1)
		f.api(
			t,
			"POST",
			base+"/credentials/enroll",
			map[string]any{"variable_id": ordinary.ID, "revision": 99},
			409,
		)
		var enrolled repository.Credential
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				base+"/credentials/enroll",
				map[string]any{"variable_id": ordinary.ID, "revision": 0},
				200,
			),
			&enrolled,
		)
		require.Equal(t, ordinary.ID, enrolled.ID)
		require.EqualValues(t, 1, enrolled.Revision)
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/variables/%d", base, ordinary.ID),
			map[string]any{
				"name":   "BYPASS",
				"secret": false,
				"value":  "must-not-write",
			},
			404,
		)
		f.api(
			t,
			"POST",
			base+"/credentials/enroll",
			map[string]any{"variable_id": ordinary.ID, "revision": 0},
			409,
		)
		kind := "variable"
		if base == sharedBase {
			kind = "lifecycle"
		}
		path := fmt.Sprintf("%s/credentials/%s/%d", base, kind, ordinary.ID)
		f.api(
			t,
			"PUT",
			path,
			map[string]any{
				"revision":   1,
				"expires_at": time.Now().Add(-time.Hour).Unix(),
			},
			200,
		)
		var summary repository.CredentialStatusSummary
		decodeLifecycleTest(
			t,
			f.api(t, "GET", f.base()+"/credentials/status", nil, 200),
			&summary,
		)
		require.Equal(t, 1, summary.Expired)
		f.api(t, "DELETE", path, map[string]any{"revision": 1}, 409)
		f.api(t, "DELETE", path, map[string]any{"revision": 2}, 204)
		decodeLifecycleTest(
			t,
			f.api(t, "GET", base+"/credentials", nil, 200),
			&rows,
		)
		require.Empty(t, rows)
		var created repository.Credential
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				base+"/credentials",
				map[string]any{
					"name":            "NEW_CREDENTIAL",
					"value":           "private-new-value",
					"rotation_due_at": time.Now().Add(24 * time.Hour).Unix(),
				},
				201,
			),
			&created,
		)
		require.Equal(t, "due_soon", created.Status)
		f.api(
			t,
			"POST",
			base+"/credentials",
			map[string]any{"name": "BAD NAME", "value": "private-new-value"},
			422,
		)
		f.api(
			t,
			"POST",
			base+"/credentials",
			map[string]any{
				"name":           "BAD_SCOPE",
				"value":          "private-new-value",
				"environment_id": -1,
			},
			422,
		)
		f.api(
			t,
			"DELETE",
			fmt.Sprintf("%s/credentials/%s/%d", base, kind, created.ID),
			map[string]any{"revision": created.Revision},
			204,
		)
	}
	var count int
	require.NoError(
		t,
		f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action IN ('create_credential','enroll_credential','delete_credential')").
			Scan(&count),
	)
	require.Equal(t, 8, count)
	var leaked int
	require.NoError(
		t,
		f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE details LIKE '%private-%'").
			Scan(&leaked),
	)
	require.Zero(t, leaked)
}
