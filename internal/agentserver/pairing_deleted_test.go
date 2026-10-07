package agentserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	agentbootstrap "github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
)

func TestPairingDeletedAgentRejectsOldCodeAfterFailedFreshAttempt(
	t *testing.T,
) {
	f := newPairingFixture(t)
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Shutdown(t.Context()) })
	offer := listener.Offer()
	raw, err := json.Marshal(offer.Code)
	if err != nil {
		t.Fatal(err)
	}
	var code string
	if err := json.Unmarshal(raw, &code); err != nil {
		t.Fatal(err)
	}
	f.input, err = ParsePairingInput(
		listener.Endpoint(), code, offer.AgentPin.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.service.connect = connectPairing
	// Interrupt cleanup: the installed bootstrap still accepts the old tuple.
	f.service.afterActivation = func() error { return errTestInterruption }
	paired, err := f.pair(t)
	if !errors.Is(err, errTestInterruption) {
		t.Fatalf("interrupted initial pairing=%+v error=%v", paired, err)
	}
	if err := f.repo.DeleteAgent(t.Context(), paired.AgentID); err != nil {
		t.Fatal(err)
	}
	f.service.afterActivation = nil
	wrong, err := ParsePairingInput(listener.Endpoint(),
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)),
		offer.AgentPin.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Pair(t.Context(), wrong); !errors.Is(
		err, ErrPairingConflict,
	) {
		t.Fatalf("incorrect fresh pairing error=%v", err)
	}
	if _, err := f.pair(t); !errors.Is(err, ErrPairingConflict) {
		t.Fatalf("deleted agent reused pre-delete code after failure: %v", err)
	}
}
