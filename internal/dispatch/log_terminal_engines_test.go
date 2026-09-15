package dispatch

import (
	"errors"
	"testing"

	"durpdeploy/internal/repository"
)

type remoteLifecycleCase struct {
	name    string
	prepare func(*repository.Repository, repository.RemoteLifecycleClaim) error
	run     func(*repository.Repository, repository.RemoteLifecycleClaim) error
}

func TestRemoteLogAndTerminalRevocationAcrossDatabases(t *testing.T) {
	cases := []remoteLifecycleCase{
		{
			name: "Log",
			run: func(repo *repository.Repository,
				identity repository.RemoteLifecycleClaim,
			) error {
				_, err := repo.AppendRemoteDeploymentLogs(
					t.Context(), identity,
					[]repository.RemoteLogEvent{{Sequence: 0, Line: "line"}},
				)
				return err
			},
		},
		{
			name: "Result",
			run: func(repo *repository.Repository,
				identity repository.RemoteLifecycleClaim,
			) error {
				_, err := repo.FinishRemoteDeploymentLifecycle(
					t.Context(), identity, "succeeded",
				)
				return err
			},
		},
		{
			name: "Cancelled",
			prepare: func(repo *repository.Repository,
				identity repository.RemoteLifecycleClaim,
			) error {
				return repo.CancelAssignedRemoteDeployment(
					t.Context(),
					repository.RemoteAssignedDeployment{
						DeploymentID: identity.DeploymentID,
						AgentID:      identity.AgentID,
					},
				)
			},
			run: func(repo *repository.Repository,
				identity repository.RemoteLifecycleClaim,
			) error {
				_, err := repo.AcknowledgeRemoteCancellation(
					t.Context(), identity,
				)
				return err
			},
		},
	}
	for _, engineName := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(engineName, func(t *testing.T) {
			engine := provisionLifecycleEngine(t, engineName)
			primary, secondary := openLifecycleEngine(t, engine)
			for caseIndex, test := range cases {
				for orderIndex, revocationFirst := range []bool{true, false} {
					order := "LifecycleFirst"
					if revocationFirst {
						order = "RevocationFirst"
					}
					t.Run(test.name+"/"+order, func(t *testing.T) {
						identity := seedRemoteLifecycle(
							t,
							primary,
							int64(
								engineIndex(
									engineName,
								)*100+caseIndex*10+orderIndex+1,
							),
						)
						if test.prepare != nil {
							if err := test.prepare(
								primary,
								identity,
							); err != nil {
								t.Fatal(err)
							}
						}
						if revocationFirst {
							revokeRemoteAgent(t, primary, identity.AgentID)
							if err := test.run(secondary, identity); !errors.Is(
								err, repository.ErrRemoteLifecycleConflict,
							) {
								t.Fatalf("revocation-first error=%v", err)
							}
							return
						}
						if err := test.run(secondary, identity); err != nil {
							t.Fatal(err)
						}
						revokeRemoteAgent(t, primary, identity.AgentID)
					})
				}
			}
		})
	}
}
