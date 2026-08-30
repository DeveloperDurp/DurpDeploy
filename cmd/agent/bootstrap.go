package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"durpdeploy/internal/agentbootstrap"
	"durpdeploy/internal/agentstate"
)

func runBootstrap(ctx context.Context, config agentbootstrap.Config) error {
	listener, err := agentbootstrap.Start(config)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		_ = listener.Shutdown(shutdownCtx)
	}()
	encoded, err := json.Marshal(listener.Offer().Code)
	if err != nil {
		return fmt.Errorf("encode pairing code: %w", err)
	}
	var code string
	if err := json.Unmarshal(encoded, &code); err != nil {
		return fmt.Errorf("decode pairing code: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Pairing code: %s\n", code)
	fmt.Fprintf(
		os.Stdout,
		"Agent fingerprint: %s\n",
		listener.Offer().AgentPin.String(),
	)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-listener.Paired():
		return nil
	case <-listener.Done():
		return agentstate.ErrRePairRequired
	}
}
