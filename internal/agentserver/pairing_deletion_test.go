package agentserver

import (
	"database/sql"
	"errors"
	"net/http"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestPairingRejectsDeletionDuringAcknowledgement(t *testing.T) {
	for _, recoverPaired := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "initial", true: "recovery"}[recoverPaired],
			func(t *testing.T) {
				// Given: either a new pairing or an already committed tuple.
				fixture := newPairingFixture(t)
				if recoverPaired {
					if _, err := fixture.pair(t); err != nil {
						t.Fatal(err)
					}
				}
				var deletedID string
				fixture.post = func(request agentproto.PairRequest) (int, error) {
					if request.CompletionAck {
						if err := fixture.repo.DeleteAgent(
							t.Context(),
							request.AgentID,
						); err != nil {
							return 0, err
						}
						deletedID = request.AgentID
					}
					return http.StatusNoContent, nil
				}

				// When: deletion commits before the completion response arrives.
				_, err := fixture.pair(t)

				// Then: pairing refuses a stale success without recreating the ID.
				if !errors.Is(err, ErrPairingConflict) {
					t.Fatalf("pairing error=%v, want conflict", err)
				}
				if _, err := fixture.repo.Queries.GetAgent(
					t.Context(),
					deletedID,
				); !errors.Is(
					err,
					sql.ErrNoRows,
				) {
					t.Fatalf("deleted registration lookup=%v", err)
				}
			},
		)
	}
}
