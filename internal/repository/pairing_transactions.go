package repository

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var ErrPairingTupleConflict = errors.New(
	"pairing tuple conflicts with durable state",
)

type AgentPairingTuple struct {
	AgentID              string
	AgentName            string
	Endpoint             string
	PairingCodeHash      []byte
	AgentPublicIdentity  string
	AgentPin             string
	ServerPublicIdentity string
	ServerPin            string
	EncryptedIdentity    string
	ExpiresAt            int64
	Now                  int64
	ServerPullEndpoint   string
	ExpectedAgentID      string
}

func (r *Repository) PrepareAgentPairing(
	ctx context.Context,
	tuple AgentPairingTuple,
) (db.AgentPairing, error) {
	var pairing db.AgentPairing
	err := r.WithTx(ctx, func(q *db.Queries) error {
		candidates, err := q.ListAgentPairingRecoveryCandidates(
			ctx,
			db.ListAgentPairingRecoveryCandidatesParams{
				PairingCodeHash: tuple.PairingCodeHash,
				AgentPin:        tuple.AgentPin,
				Endpoint:        tuple.Endpoint,
			},
		)
		if err != nil {
			return err
		}
		if tuple.ExpectedAgentID != "" {
			agent, err := q.GetAgent(ctx, tuple.ExpectedAgentID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrPairingTupleConflict
				}
				return err
			}
			for _, candidate := range candidates {
				if candidate.AgentID != tuple.ExpectedAgentID {
					return ErrPairingTupleConflict
				}
			}
			if agent.Status == "revoked" {
				pairing, err = q.ResetRevokedAgentPairing(
					ctx,
					db.ResetRevokedAgentPairingParams{
						PairingCodeHash:     tuple.PairingCodeHash,
						AgentPublicIdentity: tuple.AgentPublicIdentity,
						AgentPin:            tuple.AgentPin,
						ServerPublicIdentity: nullable(
							tuple.ServerPublicIdentity,
						),
						ServerPin:         nullable(tuple.ServerPin),
						EncryptedIdentity: nullable(tuple.EncryptedIdentity),
						ExpiresAt:         tuple.ExpiresAt,
						Now:               tuple.Now,
						ServerPullEndpoint: nullable(
							tuple.ServerPullEndpoint,
						),
						AgentID: tuple.ExpectedAgentID,
					},
				)
				if err != nil {
					return err
				}
				changed, err := q.ResetRevokedAgentForPairing(
					ctx,
					db.ResetRevokedAgentForPairingParams{
						Endpoint: tuple.Endpoint,
						ID:       tuple.ExpectedAgentID,
					},
				)
				if err != nil || changed != 1 {
					return ErrPairingTupleConflict
				}
				return nil
			}
			if len(candidates) != 1 {
				return ErrPairingTupleConflict
			}
		}
		if len(candidates) > 0 {
			if len(candidates) != 1 {
				return ErrPairingTupleConflict
			}
			candidate := candidates[0]
			if candidate.State == "expired" &&
				!samePairingCode(candidate, tuple) {
				deleted, err := q.DeleteExpiredAgentPairing(
					ctx,
					candidate.AgentID,
				)
				if err != nil || deleted != 1 {
					return ErrPairingTupleConflict
				}
				deleted, err = q.DeletePendingAgent(ctx, candidate.AgentID)
				if err != nil || deleted != 1 {
					return ErrPairingTupleConflict
				}
			} else if !samePairingTuple(
				candidate,
				tuple,
			) ||
				(candidate.State != "committing" && candidate.State != "paired") {
				return ErrPairingTupleConflict
			} else {
				pairing = candidatePairing(candidate)
				return nil
			}
		}
		if _, err := q.CreateAgent(ctx, db.CreateAgentParams{
			ID: tuple.AgentID, Name: tuple.AgentName, Endpoint: tuple.Endpoint,
		}); err != nil {
			return err
		}
		pairing, err = q.CreateCommittingAgentPairing(
			ctx,
			db.CreateCommittingAgentPairingParams{
				AgentID: tuple.AgentID, PairingCodeHash: tuple.PairingCodeHash,
				AgentPublicIdentity:  tuple.AgentPublicIdentity,
				AgentPin:             tuple.AgentPin,
				ServerPublicIdentity: nullable(tuple.ServerPublicIdentity),
				ServerPin:            nullable(tuple.ServerPin),
				EncryptedIdentity:    nullable(tuple.EncryptedIdentity),
				ExpiresAt:            tuple.ExpiresAt, Now: tuple.Now,
				ServerPullEndpoint: nullable(tuple.ServerPullEndpoint),
			},
		)
		return err
	})
	if err != nil {
		return db.AgentPairing{}, err
	}
	return pairing, nil
}

func samePairingTuple(
	pairing db.ListAgentPairingRecoveryCandidatesRow,
	tuple AgentPairingTuple,
) bool {
	return samePairingCode(pairing, tuple) &&
		pairing.AgentPublicIdentity == tuple.AgentPublicIdentity &&
		pairing.AgentPin == tuple.AgentPin &&
		pairing.ServerPublicIdentity.String == tuple.ServerPublicIdentity &&
		pairing.ServerPin.String == tuple.ServerPin &&
		pairing.ServerPullEndpoint.String == tuple.ServerPullEndpoint &&
		pairing.Endpoint == tuple.Endpoint && pairing.EncryptedIdentity.Valid
}

func samePairingCode(
	pairing db.ListAgentPairingRecoveryCandidatesRow,
	tuple AgentPairingTuple,
) bool {
	return len(pairing.PairingCodeHash) == len(tuple.PairingCodeHash) &&
		subtle.ConstantTimeCompare(
			pairing.PairingCodeHash,
			tuple.PairingCodeHash,
		) == 1
}

func candidatePairing(
	row db.ListAgentPairingRecoveryCandidatesRow,
) db.AgentPairing {
	return db.AgentPairing{
		AgentID:              row.AgentID,
		PairingCodeHash:      row.PairingCodeHash,
		AgentPublicIdentity:  row.AgentPublicIdentity,
		AgentPin:             row.AgentPin,
		ServerPublicIdentity: row.ServerPublicIdentity,
		ServerPin:            row.ServerPin,
		EncryptedIdentity:    row.EncryptedIdentity,
		State:                row.State,
		ExpiresAt:            row.ExpiresAt,
		PairedAt:             row.PairedAt,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		ServerPullEndpoint:   row.ServerPullEndpoint,
	}
}

func nullable(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
