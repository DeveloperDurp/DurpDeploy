package maintenance

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCheckBoundsFailureOutput(t *testing.T) {
	// Given: a noisy failing command with a useful final diagnostic.
	command := "head -c 1048576 /dev/zero; printf 'final diagnostic'; exit 1"
	// When: the health check fails.
	err := runCheck(t.Context(), command)
	// Then: only a bounded diagnostic tail is retained.
	if err == nil || !strings.Contains(err.Error(), "final diagnostic") {
		t.Fatalf("missing failure diagnostic: %v", err)
	}
	if len(err.Error()) > 8192 {
		t.Fatalf("failure diagnostic retained %d bytes", len(err.Error()))
	}
}

func TestRunCheckCancelsDescendants(t *testing.T) {
	// Given: a shell waits on a child that holds its output pipe open.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	// When: the health-check deadline expires.
	err := runCheck(ctx, "sleep 2 & wait")
	// Then: cancellation terminates the command and releases its output pipe.
	if err == nil {
		t.Fatal("expected deadline failure")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("canceled check waited for descendants: %s", elapsed)
	}
}

func TestStartLitestreamCheckValidatesInterval(t *testing.T) {
	for _, test := range []struct {
		interval string
		valid    bool
	}{{"", true}, {"1h", true}, {"0s", false}, {"-1s", false}, {"invalid", false}} {
		t.Run(test.interval, func(t *testing.T) {
			t.Setenv("DURPDEPLOY_LITESTREAM_CHECK_COMMAND", "true")
			t.Setenv("DURPDEPLOY_LITESTREAM_CHECK_INTERVAL", test.interval)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := StartLitestreamCheck(
				ctx,
				nil,
			); (err == nil) != test.valid {
				t.Fatalf("interval %q: %v", test.interval, err)
			}
		})
	}
}
