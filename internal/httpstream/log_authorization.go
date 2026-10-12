package httpstream

import (
	"context"
	"net/http"
	"time"

	"durpdeploy/internal/auth"
)

// watchLogAccess cancels blocked database reads too. A blocked socket write
// ends under Writer's existing 60-second deadline. With a one-second check
// cadence and one-second authorization query budget, revocation is bounded
// by 62 seconds even when the consumer stops reading.
func watchLogAccess(
	r *http.Request,
	options logStream,
) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !auth.CanStreamDeployment(
					r,
					options.repo,
					options.deploymentID,
				) {
					cancel()
					return
				}
			}
		}
	}()
	return r, cancel
}
