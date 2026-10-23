package runner

import (
	"database/sql"
	"durpdeploy/internal/testutil"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestBroadcastWriterHoldsMultilineSecretPrefixAcrossWrites(t *testing.T) {
	tests := []struct {
		chunks  []string
		secrets []string
		want    string
	}{
		{
			[]string{"prefix first\n", "second suffix\n"},
			[]string{"first\nsecond"},
			"prefix [REDACTED] suffix",
		},
		{
			[]string{"prefix first\nsecond suffix", "\n"},
			[]string{"first\nsecond"},
			"prefix [REDACTED] suffix",
		},
		{
			[]string{"prefix first\nsecond suffix", ""},
			[]string{"first\nsecond"},
			"prefix [REDACTED] suffix",
		},
		{[]string{"0Bea", "rer synthetic-proof\n"}, nil, "0[REDACTED]"},
		{[]string{"0Bearer\n", "synthetic-proof\n"}, nil, "0[REDACTED]"},
		{
			[]string{"0gh", "p_" + strings.Repeat("S", 36) + "\n"},
			nil,
			"0[REDACTED]",
		},
		{[]string{"0AK", "IAABCDEFGHIJKLMNOP\n"}, nil, "0[REDACTED]"},
		{[]string{"0xo", "xb-synthetic-proof\n"}, nil, "0[REDACTED]"},
		{[]string{"0xo", "xa-synthetic-proof\n"}, nil, "0[REDACTED]"},
		{[]string{"0xo", "xp-synthetic-proof"}, nil, "0[REDACTED]"},
	}
	for _, test := range tests {
		chunks := test.chunks
		t.Run(chunks[0], func(t *testing.T) {
			connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Close(t, connection) })
			repo := repository.New(connection)
			project, err := repo.Queries.CreateProject(
				t.Context(), db.CreateProjectParams{Name: "logs"},
			)
			if err != nil {
				t.Fatal(err)
			}
			environment, err := repo.Queries.CreateEnvironment(
				t.Context(), db.CreateEnvironmentParams{Name: "logs"},
			)
			if err != nil {
				t.Fatal(err)
			}
			release, err := repo.Queries.CreateRelease(
				t.Context(),
				db.CreateReleaseParams{
					ProjectID: project.ID, Version: "v1", StepsJson: "[]",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := repo.Queries.CreateDeployment(
				t.Context(),
				db.CreateDeploymentParams{
					ReleaseID: release.ID, EnvironmentID: environment.ID,
					Status: "running",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			broker := NewLogBroker()
			if _, err := repo.DB.ExecContext(t.Context(), `INSERT INTO deployment_step_sources(deployment_id,steps_json) VALUES(?,'[]')`, deployment.ID); err != nil {
				t.Fatal(err)
			}
			stream := broker.Subscribe(deployment.ID)
			t.Cleanup(func() { broker.Unsubscribe(deployment.ID, stream) })
			writer := &broadcastWriter{
				broker: broker, repo: repo, deploymentID: deployment.ID,
				stepName: "secret", ctx: t.Context(),
				scrubber: NewScrubber(test.secrets),
			}

			if _, err := writer.Write([]byte(chunks[0])); err != nil {
				t.Fatal(err)
			}
			select {
			case line := <-stream:
				t.Fatalf("secret prefix was broadcast early: %q", line)
			default:
			}
			if _, err := writer.Write([]byte(chunks[1])); err != nil {
				t.Fatal(err)
			}
			writer.Flush()
			if line := <-stream; line != test.want {
				t.Fatalf("broadcast line=%q", line)
			}
			logs, err := repo.Queries.ListDeploymentLogsByDeployment(
				t.Context(), deployment.ID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(logs) != 1 || logs[0].Line != test.want ||
				logs[0].StepName != (sql.NullString{String: "secret", Valid: true}) {
				t.Fatalf("stored logs=%+v", logs)
			}
		})
	}
}
