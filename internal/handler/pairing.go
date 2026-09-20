package handler

import (
	"net/http"

	"durpdeploy/internal/agentserver"
)

func PairingPOSTHandler(
	pairing *agentserver.PairingService,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pairing == nil {
			markSafeErrorResponse(w)
			http.Error(
				w,
				"Agent listener is disabled",
				http.StatusServiceUnavailable,
			)
			return
		}
		next.ServeHTTP(w, r)
	})
}
