package repository_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestAgentConcurrentCapacity(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	start := make(chan struct{})
	var counts [2]int64
	var errs [2]error
	for i := range counts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			a := claimArg("a")
			a.DeploymentID = int64(i + 1)
			a.ClaimTokenHash[0] = byte(i + 1)
			counts[i], errs[i] = r.ClaimRemoteDeployment(ctx, a)
		}()
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || counts[0]+counts[1] != 1 {
		t.Fatalf("claims=%v errors=%v", counts, errs)
	}
	var occupied int
	err := r.DB.QueryRow(`SELECT COUNT(*) FROM remote_deployment_claims
WHERE agent_id = 'a' AND state = 'claimed'`).Scan(&occupied)
	if err != nil || occupied != 1 {
		t.Fatalf("occupied=%d error=%v", occupied, err)
	}
	t.Logf(
		"same agent, different deployments: claims=%v occupied=%d",
		counts,
		occupied,
	)
}

func TestPairingCommitRollbackAndStaleIdentity(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	_, err := r.DB.Exec(`UPDATE agent_pairings SET state = 'committing',
paired_at = NULL WHERE agent_id = 'a'`)
	if err != nil {
		t.Fatal(err)
	}
	p := db.CompleteAgentPairingParams{
		AgentID: "a", Now: ni(101), ServerPin: ns(strings.Repeat("a", 64)),
	}
	n, err := r.CommitAgentPairing(ctx, p, db.ActivatePairedAgentParams{})
	assertZero(t, n, err)
	row, err := r.Queries.GetAgentPairing(ctx, "a")
	if err != nil || row.State != "committing" || row.PairedAt.Valid {
		t.Fatalf("rollback state=%s paired=%v error=%v",
			row.State, row.PairedAt.Valid, err)
	}
	p.ServerPin = ns(strings.Repeat("b", 64))
	n, err = r.CommitAgentPairing(ctx, p, db.ActivatePairedAgentParams{})
	assertZero(t, n, err)
	p.ServerPin, p.Now = row.ServerPin, ni(500)
	n, err = r.CommitAgentPairing(ctx, p, db.ActivatePairedAgentParams{})
	assertZero(t, n, err)
	t.Log(
		"activation failure=0; persisted state=committing paired_at=NULL; wrong pin=0; expiry=0",
	)
}

func TestAgentProjectMembershipIntegerContract(t *testing.T) {
	runProjectMembershipIntegerContract(t, remoteFixture(t))
}

func runProjectMembershipIntegerContract(
	t *testing.T,
	r *repository.Repository,
) {
	t.Helper()
	ctx := context.Background()
	u, err := r.Queries.CreateUser(ctx, db.CreateUserParams{
		Email: "membership@example.invalid", Name: "fixture",
		PasswordHash: "fixture", Role: "viewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	arg := db.IsProjectMemberParams{ProjectID: 1, UserID: u.ID}
	for _, want := range []int64{0, 1, 0} {
		got, err := r.Queries.IsProjectMember(ctx, arg)
		if err != nil || got != want {
			t.Fatalf("membership=%d want=%d error=%v", got, want, err)
		}
		if want == 0 {
			err = r.Queries.AddProjectMember(ctx, db.AddProjectMemberParams{
				ProjectID: 1, UserID: u.ID, Role: "deployer",
			})
		} else {
			err = r.Queries.RemoveProjectMember(
				ctx,
				db.RemoveProjectMemberParams(arg),
			)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Log("membership int64: absent=0 member=1 removed=0")
}
