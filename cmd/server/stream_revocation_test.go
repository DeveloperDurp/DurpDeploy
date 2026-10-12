package main

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestOpenLogStreamsStopAfterMembershipRevocation(t *testing.T) {
	for routeIndex := 0; routeIndex < 7; routeIndex++ {
		for _, structured := range []bool{false, true} {
			if structured && (routeIndex == 2 || routeIndex == 6) {
				continue
			}
			t.Run(
				fmt.Sprintf("route%d/structured=%v", routeIndex, structured),
				func(t *testing.T) {
					// Given: a project member opens a browser/API ordinary/runbook log stream.
					f := newStreamFixture(t)
					route := f.paths[routeIndex]
					if structured {
						route.path += "?format=structured"
					}
					_, err := f.repo.DB.ExecContext(
						t.Context(),
						"UPDATE users SET role='deployer' WHERE id=1",
					)
					require.NoError(t, err)
					require.NoError(
						t,
						f.repo.Queries.AddProjectMember(
							t.Context(),
							db.AddProjectMemberParams{
								ProjectID: 1,
								UserID:    1,
								Role:      "deployer",
							},
						),
					)
					srv := httptest.NewServer(f.router)
					defer srv.Close()
					req, err := http.NewRequestWithContext(
						t.Context(),
						"GET",
						srv.URL+route.path,
						nil,
					)
					require.NoError(t, err)
					req.Header.Set("Authorization", "Bearer ddp_pat_timeout")
					req.AddCookie(
						&http.Cookie{Name: "session", Value: "timeout-session"},
					)
					response, err := srv.Client().Do(req)
					require.NoError(t, err)
					defer response.Body.Close()
					require.Equal(t, http.StatusOK, response.StatusCode)
					scanner := bufio.NewScanner(response.Body)
					for scanner.Scan() {
						if strings.Contains(scanner.Text(), "historical") {
							break
						}
					}
					// When: the public membership mutation removes access from the open stream.
					// Use an administrator's separate request through the full API router.
					admin, err := f.repo.Queries.CreateUser(
						t.Context(),
						db.CreateUserParams{
							Email:        "revoker@example.com",
							Name:         "Revoker",
							PasswordHash: "unused",
							Role:         "admin",
						},
					)
					require.NoError(t, err)
					_, err = f.repo.Queries.CreateApiToken(
						t.Context(),
						db.CreateApiTokenParams{
							ID:        "revoker",
							UserID:    admin.ID,
							Name:      "Revoker",
							Scope:     "global",
							TokenHash: auth.HashApiToken("ddp_pat_revoker"),
						},
					)
					require.NoError(t, err)
					remove, err := http.NewRequestWithContext(
						t.Context(),
						"DELETE",
						srv.URL+"/api/v1/projects/1/members/1",
						nil,
					)
					require.NoError(t, err)
					remove.Header.Set("Authorization", "Bearer ddp_pat_revoker")
					removed, err := srv.Client().Do(remove)
					require.NoError(t, err)
					removed.Body.Close()
					require.Equal(t, http.StatusNoContent, removed.StatusCode)
					// Then: the stream closes promptly, and output appended after closure cannot leak.
					ended := make(chan struct{})
					go func() {
						for scanner.Scan() {
						}
						close(ended)
					}()
					select {
					case <-ended:
					case <-time.After(3 * time.Second):
						t.Fatal("membership revocation did not close stream")
					}
					_, err = f.repo.Queries.CreateDeploymentLog(
						t.Context(),
						db.CreateDeploymentLogParams{
							DeploymentID: route.id,
							Line:         "after-revocation",
						},
					)
					require.NoError(t, err)
					f.broker.Broadcast(route.id, "after-revocation")
					denied, err := srv.Client().Do(req)
					require.NoError(t, err)
					denied.Body.Close()
					require.NotEqual(t, http.StatusOK, denied.StatusCode)
				},
			)
		}
	}
}
