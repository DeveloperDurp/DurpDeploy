package repository_test

import (
	"fmt"
	"math"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRemoteClaimRejectsInvalidExecutionLimits(t *testing.T) {
	for _, tc := range []struct {
		name             string
		timeout, retries int64
	}{
		{"timeout", 86401, 0}, {"retries", 0, 11},
		{"timeout overflow", math.MaxInt64, 0}, {"retries overflow", 0, math.MaxInt64},
	} {
		for _, whole := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("%s/whole=%t", tc.name, whole),
				func(t *testing.T) {
					checkRemoteExecutionLimitClaim(
						t,
						tc.timeout,
						tc.retries,
						whole,
					)
				},
			)
		}
	}
}

func checkRemoteExecutionLimitClaim(
	t *testing.T,
	timeout, retries int64,
	whole bool,
) {
	t.Helper()
	r := remoteFixture(t)
	id := int64(1)
	if !whole {
		id = 3
		if _, err := r.DB.ExecContext(t.Context(), `
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('a',2);
INSERT INTO agent_labels(agent_id,label) VALUES('a','other');
UPDATE deployments SET status='running' WHERE id=3;`); err != nil {
			t.Fatal(err)
		}
		if n, err := r.QueueRemoteStepRuns(t.Context(), id, 0); err != nil ||
			n != 1 {
			t.Fatalf("queue=%d: %v", n, err)
		}
	}
	// The later step is invalid. Claiming step zero must validate it too.
	if _, err := r.DB.ExecContext(t.Context(), `UPDATE deployment_steps SET timeout_seconds=?,max_retries=? WHERE deployment_id=? AND step_index=1`, timeout, retries, id); err != nil {
		t.Fatal(err)
	}
	prepare := func(repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
		t.Fatal("invalid snapshot reached payload preparation")
		return repository.RemotePreparedClaim{}, nil
	}
	claim := r.ClaimRemoteStepPayload
	if whole {
		claim = r.ClaimRemoteDeploymentPayload
	}
	payload, claimed, err := claim(t.Context(), "a", prepare)
	if err != nil || claimed || payload.Token != "" ||
		len(payload.Ciphertext) != 0 {
		t.Fatalf(
			"claim=%+v claimed=%t: %v",
			payload,
			claimed,
			err,
		)
	}
	stored, err := r.Queries.GetDeployment(t.Context(), id)
	if err != nil || stored.Status != "failed" ||
		!stored.FinishedAt.Valid {
		t.Fatalf("deployment=%+v: %v", stored, err)
	}
	checkFailedRemoteExecutionLimitClaim(t, r, id, whole)
}

func checkFailedRemoteExecutionLimitClaim(
	t *testing.T,
	r *repository.Repository,
	id int64,
	whole bool,
) {
	t.Helper()
	if whole {
		row, err := r.Queries.GetRemoteDeploymentClaim(
			t.Context(),
			id,
		)
		if err != nil || row.State != "failed" ||
			row.Reason.String != "remote_execution_capability_unavailable" ||
			len(row.ClaimTokenHash) != 0 ||
			row.Ciphertext.Valid {
			t.Fatalf("claim row=%+v: %v", row, err)
		}
		// The next queued deployment remains claimable on the next poll.
		_, ok, err := r.ClaimRemoteDeploymentPayload(
			t.Context(),
			"a",
			func(repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
				return repository.RemotePreparedClaim{
					Token:      "next",
					TokenHash:  make([]byte, 32),
					Ciphertext: []byte("next"),
				}, nil
			},
		)
		if err != nil || !ok {
			t.Fatalf("next claim=%t: %v", ok, err)
		}
	} else {
		runs, err := r.Queries.ListRemoteStepRuns(t.Context(), db.ListRemoteStepRunsParams{DeploymentID: id, StepIndex: 0})
		if err != nil || len(runs) != 1 || runs[0].State != "failed" || len(runs[0].ClaimTokenHash) != 0 || runs[0].Ciphertext.Valid {
			t.Fatalf("runs=%+v: %v", runs, err)
		}
	}
}
