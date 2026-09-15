package agentserver

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
	"github.com/google/uuid"
)

const pairingTTL = 10 * time.Minute

type PairingConfig struct {
	Repository   *repository.Repository
	Identity     agenttls.Identity
	PullEndpoint agentproto.PullEndpoint
	Secrets      *secret.Box
	Now          func() time.Time
}

type PairingService struct {
	repository      *repository.Repository
	identity        agenttls.Identity
	pullEndpoint    agentproto.PullEndpoint
	secrets         *secret.Box
	now             func() time.Time
	connect         func(context.Context, string, agenttls.Identity) (pairingConnection, error)
	afterPersist    func() error
	afterFirstPhase func() error
	afterActivation func() error
}

func NewPairingService(config PairingConfig) (*PairingService, error) {
	if config.Repository == nil || config.Secrets == nil || config.Now == nil ||
		config.PullEndpoint.String() == "" {
		return nil, errors.New("pairing service requires durable configuration")
	}
	if _, err := agenttls.NewServerConfig(
		config.Identity,
		"",
		agenttls.Fingerprint{},
	); err != nil {
		return nil, err
	}
	return &PairingService{
		repository: config.Repository, identity: config.Identity,
		pullEndpoint: config.PullEndpoint, secrets: config.Secrets,
		now: config.Now, connect: connectPairing,
	}, nil
}

func (service *PairingService) Pair(
	ctx context.Context,
	input PairingInput,
) (PairingResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	connection, err := service.connect(
		requestCtx,
		input.address,
		service.identity,
	)
	if err != nil {
		return PairingResult{}, fmt.Errorf("observe agent certificate: %w", err)
	}
	defer connection.Close()
	observedDER := connection.CertificateDER()
	observedPin := agenttls.FingerprintOf(observedDER)
	if subtle.ConstantTimeCompare(observedPin[:], input.fingerprint[:]) != 1 {
		return PairingResult{}, ErrPairingFingerprint
	}
	pairing, err := service.prepare(
		requestCtx,
		input,
		observedDER,
		observedPin,
	)
	if err != nil {
		if errors.Is(err, repository.ErrPairingTupleConflict) {
			return PairingResult{}, ErrPairingConflict
		}
		return PairingResult{}, fmt.Errorf("persist pairing tuple: %w", err)
	}
	result := PairingResult{AgentID: pairing.AgentID, State: pairing.State}
	if service.afterPersist != nil {
		if err := service.afterPersist(); err != nil {
			return result, err
		}
	}
	request := service.protocolRequest(input.code, pairing, false)
	if pairing.State == PairingStatePaired {
		request.CompletionAck = true
		return service.cleanup(requestCtx, connection, request, result)
	}
	status, err := connection.Post(requestCtx, request)
	if err != nil {
		return result, err
	}
	if status != http.StatusNoContent {
		if status == http.StatusUnauthorized || status == http.StatusGone {
			_, expireErr := service.repository.Queries.ExpireCommittingAgentPairing(
				requestCtx,
				db.ExpireCommittingAgentPairingParams{
					Now: service.now().Unix(), AgentID: pairing.AgentID,
				},
			)
			if expireErr != nil {
				return result, fmt.Errorf(
					"expire rejected pairing: %w",
					expireErr,
				)
			}
			return result, ErrPairingConflict
		}
		return result, ErrPairingUnavailable
	}
	if service.afterFirstPhase != nil {
		if err := service.afterFirstPhase(); err != nil {
			return result, err
		}
	}
	if err := service.activate(
		requestCtx,
		pairing,
		service.now().Unix(),
	); err != nil {
		return result, err
	}
	result.State = PairingStatePaired
	if service.afterActivation != nil {
		if err := service.afterActivation(); err != nil {
			return result, err
		}
	}
	request.CompletionAck = true
	return service.cleanup(requestCtx, connection, request, result)
}

func (service *PairingService) prepare(
	ctx context.Context,
	input PairingInput,
	agentDER []byte,
	agentPin agenttls.Fingerprint,
) (db.AgentPairing, error) {
	now := service.now()
	agentPEM := string(pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: agentDER},
	))
	serverPEM := string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: service.identity.Certificate.Certificate[0],
	}))
	encryptedIdentity, err := service.secrets.Encrypt(agentPEM)
	if err != nil {
		return db.AgentPairing{}, fmt.Errorf("encrypt agent identity: %w", err)
	}
	codeHash := input.code.Hash()
	agentID := uuid.NewString()
	return service.repository.PrepareAgentPairing(
		ctx,
		repository.AgentPairingTuple{
			AgentID: agentID, AgentName: "agent-" + agentID[:8],
			Endpoint: input.address, PairingCodeHash: codeHash[:],
			AgentPublicIdentity: agentPEM, AgentPin: agentPin.String(),
			ServerPublicIdentity: serverPEM,
			ServerPin:            service.identity.Fingerprint.String(),
			EncryptedIdentity:    encryptedIdentity,
			ExpiresAt:            now.Add(pairingTTL).Unix(), Now: now.Unix(),
			ServerPullEndpoint: service.pullEndpoint.String(),
			ExpectedAgentID:    input.expectedAgentID,
		},
	)
}

func (service *PairingService) protocolRequest(
	code agentproto.PairingCode,
	pairing db.AgentPairing,
	completionAck bool,
) agentproto.PairRequest {
	agentPin, _ := agentproto.ParseSHA256Pin(pairing.AgentPin)
	serverPin, _ := agentproto.ParseSHA256Pin(pairing.ServerPin.String)
	return agentproto.PairRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: agentproto.AgentV1,
		},
		PairingCode: code, AgentPin: agentPin, ServerPin: serverPin,
		PullEndpoint: service.pullEndpoint, AgentID: pairing.AgentID,
		CompletionAck: completionAck,
	}
}

func (service *PairingService) activate(
	ctx context.Context,
	pairing db.AgentPairing,
	now int64,
) error {
	changed, err := service.repository.CommitAgentPairing(
		ctx,
		db.CompleteAgentPairingParams{
			Now:     sql.NullInt64{Int64: now, Valid: true},
			AgentID: pairing.AgentID, ServerPin: pairing.ServerPin,
		},
		db.ActivatePairedAgentParams{
			CertificatePem: sql.NullString{
				String: pairing.AgentPublicIdentity, Valid: true,
			},
			CertificateFingerprint: sql.NullString{
				String: pairing.AgentPin, Valid: true,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("activate paired agent: %w", err)
	}
	if changed != 1 {
		return ErrPairingConflict
	}
	return nil
}

func (service *PairingService) cleanup(
	ctx context.Context,
	connection pairingConnection,
	request agentproto.PairRequest,
	result PairingResult,
) (PairingResult, error) {
	status, err := connection.Post(ctx, request)
	if err != nil {
		return result, err
	}
	if status != http.StatusNoContent {
		return result, ErrPairingUnavailable
	}
	return result, nil
}
