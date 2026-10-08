//go:build e2e

package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

type delayedGateScanDB struct {
	*sql.DB
	paused  atomic.Bool
	started chan struct{}
	resume  chan struct{}
}

func (d *delayedGateScanDB) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if strings.Contains(query, "-- name: GetArtifactGateChunk") &&
		!d.paused.Swap(true) {
		close(d.started)
		select {
		case <-d.resume:
		case <-ctx.Done():
		}
	}
	return d.DB.QueryRowContext(ctx, query, args...)
}

func TestArtifactGateScanAllowsConcurrentWritesE2E(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint("reject=", reject), func(t *testing.T) {
			f, deployment, gate := newGateDeployment(t)
			scan := &delayedGateScanDB{
				DB:      f.h.repo.DB,
				started: make(chan struct{}),
				resume:  make(chan struct{}),
			}
			f.h.repo.Queries = db.New(scan)
			var once sync.Once
			unblock := func() { once.Do(func() { close(scan.resume) }) }
			defer unblock()
			body, err := json.Marshal(
				map[string]any{"revision": 1, "sha256": gate.SHA256},
			)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(
				t.Context(),
				"POST",
				f.baseURL+gateAPIPath(deployment.ID)+"/0/approve",
				bytes.NewReader(body),
			)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+f.token)
			request.Header.Set("Content-Type", "application/json")
			result := make(chan int, 1)
			go func() {
				response, err := f.client.Do(request)
				if err != nil {
					result <- 0
					return
				}
				defer response.Body.Close()
				result <- response.StatusCode
			}()
			select {
			case <-scan.started:
			case status := <-result:
				t.Fatalf("approval bypassed delayed scan: %d", status)
			case <-time.After(5 * time.Second):
				t.Fatal("bundle scan did not start")
			}
			// Slow immutable chunk reads must leave writes and rejection available.
			f.api(
				t,
				"PUT",
				f.base(),
				map[string]string{"name": "Write during review"},
				200,
			)
			want := 200
			if reject {
				f.api(
					t,
					"POST",
					gateAPIPath(deployment.ID)+"/0/reject",
					map[string]any{"revision": 1, "sha256": gate.SHA256},
					200,
				)
				want = 409
			}
			unblock()
			select {
			case status := <-result:
				if status != want {
					t.Fatalf("approval=%d want=%d", status, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("approval did not complete")
			}
			if !reject {
				f.completion(t, deployment.ID, events.DeploymentSucceeded)
			}
		})
	}
}

func TestArtifactGateReservedVariableE2E(t *testing.T) {
	f := newArtifactE2E(t)
	path := f.base() + "/variables"
	webPath := fmt.Sprintf("/projects/%d/variables", f.project.ID)
	f.api(
		t,
		"POST",
		path,
		map[string]any{"name": "DURPDEPLOY_APPROVED_DIR", "value": "untrusted"},
		422,
	)
	f.web(
		t,
		"POST",
		webPath,
		url.Values{"name": {"DURPDEPLOY_APPROVED_DIR"}, "value": {"untrusted"}},
		422,
	)
	for _, secret := range []bool{false, true} {
		var variable struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(
			f.api(
				t,
				"POST",
				path,
				map[string]any{
					"name":   fmt.Sprint("KEEP_", secret),
					"value":  "keep",
					"secret": secret,
				},
				201,
			),
			&variable,
		); err != nil {
			t.Fatal(err)
		}
		value := "changed"
		if secret {
			value = ""
		}
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/%d", path, variable.ID),
			map[string]any{
				"name":   "DURPDEPLOY_APPROVED_DIR",
				"value":  value,
				"secret": secret,
			},
			422,
		)
		form := url.Values{
			"name":  {"DURPDEPLOY_APPROVED_DIR"},
			"value": {value},
		}
		if secret {
			form.Set("secret", "on")
		}
		f.web(t, "PUT", fmt.Sprintf("%s/%d", webPath, variable.ID), form, 422)
		stored, err := f.h.repo.GetVariable(t.Context(), variable.ID)
		if err != nil || stored.Name != fmt.Sprint("KEEP_", secret) ||
			stored.Value.String != "keep" {
			t.Fatalf("reserved rename changed variable=%+v err=%v", stored, err)
		}
	}
}
