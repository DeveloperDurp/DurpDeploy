//go:build e2e && agentstaging

package agentserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func (s *stagingE2E) assertRuntimeStagingPurged(t *testing.T) {
	t.Helper()
	s.assertRuntimeStagingPurgedContext(t.Context(), t)
}

func (s *stagingE2E) assertRuntimeStagingPurgedContext(
	ctx context.Context, t *testing.T,
) {
	t.Helper()
	filters := []string{
		"io.durpdeploy.namespace=" + os.Getenv(
			"DURPDEPLOY_CONTAINER_NAMESPACE",
		),
	}
	for index, directory := range s.directories {
		id := []string{"test-agent", "second-agent"}[index]
		identity, err := agenttls.LoadExisting(directory)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(
			[]string{
				id,
				s.f.server.URL + "\x00" + identity.Fingerprint.String(),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(encoded)
		filters = append(
			filters,
			"io.durpdeploy.agentstage=agent-"+hex.EncodeToString(digest[:]),
		)
	}
	for _, filter := range filters {
		output, err := exec.CommandContext(ctx, "podman", "--remote", "--url="+os.Getenv("AGENT_TEST_SOCKET"), "volume", "ls", "--quiet", "--filter=label="+filter).
			CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != "" {
			t.Fatalf(
				"staging volumes retained for %s: %s (%v)",
				filter,
				output,
				err,
			)
		}
	}
	var chunks int
	if err := s.f.repo.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployment_staging_chunks").Scan(&chunks); err != nil ||
		chunks != 0 {
		t.Fatalf("encrypted staging chunks retained: %d (%v)", chunks, err)
	}
}
