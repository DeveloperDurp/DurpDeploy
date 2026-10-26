package agentexecution

import (
	"context"
	"net/http"

	"durpdeploy/internal/config"
)

type executionLimitsKey struct{}

// WithRequestLimits captures the router policy for template input bounds.
func WithRequestLimits(
	limits config.ExecutionLimits,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), executionLimitsKey{}, limits)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestLimits preserves defaults for templates rendered outside a router.
func RequestLimits(ctx context.Context) config.ExecutionLimits {
	if limits, ok := ctx.Value(executionLimitsKey{}).(config.ExecutionLimits); ok {
		return limits
	}
	return config.Defaults().Limits.Execution
}
