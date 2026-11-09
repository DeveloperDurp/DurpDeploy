package repository_test

import (
	"testing"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentPollUsesDatabaseClockInsteadOfCallerClock(t *testing.T) {
	for _, supplied := range []int64{1, 1 << 60} {
		repo := remoteFixture(t)
		agent, err := repo.Queries.GetAgent(t.Context(), "a")
		if err != nil {
			t.Fatal(err)
		}
		before, err := repo.Queries.CurrentUnixTime(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		changed, err := repo.RecordAgentPoll(
			t.Context(),
			db.HeartbeatAgentParams{
				ID: "a", Now: ni(supplied),
				CertificateFingerprint: agent.CertificateFingerprint,
			},
			agentproto.PollRequest{
				ProtocolEnvelope: agentproto.ProtocolEnvelope{
					Protocol: agentproto.AgentV1,
				},
			},
		)
		if err != nil || changed != 1 {
			t.Fatalf(
				"poll with caller clock %d: rows=%d err=%v",
				supplied,
				changed,
				err,
			)
		}
		updated, err := repo.Queries.GetAgent(t.Context(), "a")
		if err != nil {
			t.Fatal(err)
		}
		after, err := repo.Queries.CurrentUnixTime(t.Context())
		if err != nil || !updated.LastHeartbeatAt.Valid ||
			updated.LastHeartbeatAt.Int64 < before || updated.LastHeartbeatAt.Int64 > after {
			t.Fatalf(
				"database interval=[%d,%d] heartbeat=%v err=%v",
				before,
				after,
				updated.LastHeartbeatAt,
				err,
			)
		}
	}
}
