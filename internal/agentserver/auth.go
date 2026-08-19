package agentserver

import (
	"context"
	"database/sql"
	"net/http"

	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/db"
)

type authenticatedAgentKey struct{}

func (server *Server) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.TLS == nil || len(request.TLS.PeerCertificates) != 1 {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			fingerprint := agenttls.FingerprintOf(
				request.TLS.PeerCertificates[0].Raw,
			).String()
			agent, err := server.repository.Queries.GetActiveAgentByFingerprint(
				request.Context(),
				sql.NullString{String: fingerprint, Valid: true},
			)
			if err != nil {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(
				writer,
				request.WithContext(context.WithValue(
					request.Context(), authenticatedAgentKey{}, agent,
				)),
			)
		},
	)
}

func agentFromContext(ctx context.Context) db.GetActiveAgentByFingerprintRow {
	return ctx.Value(authenticatedAgentKey{}).(db.GetActiveAgentByFingerprintRow)
}
