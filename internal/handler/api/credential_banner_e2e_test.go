//go:build e2e

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestCredentialBannerInheritanceAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lifecycle, stage struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Banner inheritance"},
			201,
		),
		&lifecycle,
	)
	base := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle.ID)
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			base+"/stages",
			map[string]int64{"environment_id": f.environment.ID},
			201,
		),
		&stage,
	)
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycle.ID},
		200,
	)
	f.api(
		t,
		"POST",
		base+"/credentials",
		map[string]any{
			"name":       "GLOBAL_TOKEN",
			"value":      "private-global",
			"expires_at": 1,
		},
		201,
	)
	f.api(
		t,
		"POST",
		base+"/credentials",
		map[string]any{
			"name":           "SCOPED_TOKEN",
			"value":          "private-scoped",
			"expires_at":     1,
			"environment_id": f.environment.ID,
		},
		201,
	)
	assertExpired := func(count int) {
		t.Helper()
		var summary repository.CredentialStatusSummary
		decodeLifecycleTest(
			t,
			f.api(t, "GET", f.base()+"/credentials/status", nil, 200),
			&summary,
		)
		require.Equal(t, count, summary.Expired)
	}
	assertExpired(2)
	// Ordinary overrides also suppress the matching shared credential.
	f.api(
		t,
		"POST",
		f.base()+"/variables",
		map[string]any{
			"name":   "GLOBAL_TOKEN",
			"value":  "private-override",
			"secret": true,
		},
		201,
	)
	assertExpired(1)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/stages/%d/delete", base, stage.ID),
		nil,
		204,
	)
	assertExpired(0)
	var rows []repository.Credential
	decodeLifecycleTest(
		t,
		f.api(t, "GET", f.base()+"/credentials", nil, 200),
		&rows,
	)
	require.Empty(t, rows)
	// Restore the stage and retain an inherited warning for manual checks.
	f.api(
		t,
		"POST",
		base+"/stages",
		map[string]int64{"environment_id": f.environment.ID},
		201,
	)
	assertExpired(1)
}
