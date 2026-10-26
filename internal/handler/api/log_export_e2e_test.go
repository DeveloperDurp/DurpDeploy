//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/repository"
)

type textExportTarget struct {
	deploymentID     int64
	apiPath, webPath string
}

func seedTextExportTargets(t *testing.T, f *artifactE2E) []textExportTarget {
	t.Helper()
	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(
		t,
		f.h.repo,
		release.ID,
		f.environment.ID,
		"cancelled",
	)
	book, _, err := f.h.repo.SaveRunbook(t.Context(), repository.RunbookSave{
		ProjectID: f.project.ID, Name: "text-export", StepsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, result, err := f.h.repo.CreateRunbookExecution(t.Context(),
		repository.RunbookExecutionRequest{ProjectID: f.project.ID,
			RunbookID: book.ID, EnvironmentID: f.environment.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET status='succeeded' WHERE id=?", result.Deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	return []textExportTarget{
		{deployment.ID,
			fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deployment.ID),
			fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID)},
		{result.Deployment.ID,
			fmt.Sprintf(
				"%s/runbook-executions/%d/logs.txt",
				f.base(),
				execution.ID,
			),
			fmt.Sprintf(
				"/projects/%d/runbooks/executions/%d/logs.txt",
				f.project.ID,
				execution.ID,
			)},
	}
}

func TestTextLogExportAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	targets := seedTextExportTargets(t, f)
	for _, target := range targets {
		t.Run(target.apiPath, func(t *testing.T) {
			header := textExportHeader(t, f, target.deploymentID)
			assertTextExportPair(t, f, target, header+"\n")
			body := seedTextExportLogs(t, f, target.deploymentID)
			assertTextExportPair(t, f, target, header+body+"\n")
		})
	}
	assertLogPageAccess(t, f,
		strings.TrimSuffix(targets[0].apiPath, ".txt"),
		strings.TrimSuffix(targets[0].webPath, "/logs.txt"))
	// Metadata failure occurs before response headers and retains each envelope.
	renameTextExportTable(t, f, "environments", "export_hidden_environments")
	for _, api := range []bool{false, true} {
		path := targets[0].webPath
		if api {
			path = targets[0].apiPath
		}
		headers, body := readTextExport(t, f, path, 500)
		if headers.Get("Content-Disposition") != "" ||
			!strings.Contains(strings.ToLower(body), "internal server error") ||
			strings.Contains(body, "environments") ||
			strings.HasPrefix(
				headers.Get("Content-Type"),
				"application/json",
			) != api {
			t.Fatalf(
				"metadata failure changed transport error rendering: %v %q",
				headers,
				body,
			)
		}
	}
	renameTextExportTable(t, f, "export_hidden_environments", "environments")
	// Storage failure after the header stops the file without an error envelope.
	renameTextExportTable(t, f, "deployment_logs", "export_hidden_logs")
	for _, target := range targets {
		assertTextExportPair(t, f, target,
			textExportHeader(t, f, target.deploymentID))
	}
}

func renameTextExportTable(t *testing.T, f *artifactE2E, from, to string) {
	t.Helper()
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"ALTER TABLE "+from+" RENAME TO "+to,
	); err != nil {
		t.Fatal(err)
	}
}

func textExportHeader(t *testing.T, f *artifactE2E, id int64) string {
	t.Helper()
	deployment, err := f.h.repo.Queries.GetDeployment(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	release, err := f.h.repo.Queries.GetRelease(
		t.Context(),
		deployment.ReleaseID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(
		"=== deployment #%d | project=%s | release=%s | env=%s | status=%s ===\n",
		id,
		f.project.Name,
		release.Version,
		f.environment.Name,
		deployment.Status,
	)
}

func seedTextExportLogs(t *testing.T, f *artifactE2E, id int64) string {
	t.Helper()
	var expected strings.Builder
	for index := range 304 {
		step := sql.NullString{String: "build", Valid: true}
		line := fmt.Sprintf("line-%03d", index)
		formatted := fmt.Sprintf("[2000-01-01 00:00:00] [build] %s\n", line)
		switch index {
		case 0:
			line = "first 界\r\nsecond line"
			formatted = "[2000-01-01 00:00:00] [build] first 界\r\nsecond line\n"
		case 1:
			step = sql.NullString{}
			line = "without step"
			formatted = "[2000-01-01 00:00:00] without step\n"
		case 2:
			step.String = ""
			line = "empty step"
			formatted = "[2000-01-01 00:00:00] [] empty step\n"
		case 3:
			step = sql.NullString{}
			line = ""
			formatted = "[2000-01-01 00:00:00] \n"
		}
		if _, err := f.h.repo.DB.ExecContext(t.Context(),
			"INSERT INTO deployment_logs (deployment_id,step_name,line,created_at) VALUES (?,?,?,946684800)",
			id, step, line); err != nil {
			t.Fatal(err)
		}
		expected.WriteString(formatted)
	}
	return expected.String()
}

func assertTextExportPair(
	t *testing.T, f *artifactE2E, target textExportTarget, want string,
) {
	t.Helper()
	for _, path := range []string{target.apiPath, target.webPath} {
		headers, body := readTextExport(t, f, path, 200)
		if body != want {
			t.Fatalf(
				"%s: exported bytes differ: got %q, want %q",
				path,
				body,
				want,
			)
		}
		if headers.Get("Content-Type") != "text/plain; charset=utf-8" ||
			headers.Get("Content-Disposition") != fmt.Sprintf(
				`attachment; filename="deployment-%d.log"`,
				target.deploymentID,
			) {
			t.Fatalf("%s: download headers changed: %v", path, headers)
		}
	}
}

func readTextExport(
	t *testing.T, f *artifactE2E, path string, want int,
) (http.Header, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		"GET",
		f.baseURL+path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, "/api/") {
		req.Header.Set("Authorization", "Bearer "+f.token)
	} else {
		req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	}
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf(
			"%s: status=%d want=%d body=%s",
			path,
			response.StatusCode,
			want,
			body,
		)
	}
	return response.Header, string(body)
}
