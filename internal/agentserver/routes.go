package agentserver

import (
	"net/http"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Handler returns the dedicated agent router.
func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(
		agentproto.PollPath,
		server.authenticated(http.HandlerFunc(server.poll)),
	)
	mux.Handle(
		agentproto.StartPath,
		server.authenticated(http.HandlerFunc(server.start)),
	)
	mux.Handle(
		agentproto.HeartbeatPath,
		server.authenticated(http.HandlerFunc(server.heartbeat)),
	)
	mux.Handle(
		agentproto.LogsPath,
		server.authenticated(http.HandlerFunc(server.logs)),
	)
	mux.Handle(
		agentproto.ResultPath,
		server.authenticated(http.HandlerFunc(server.result)),
	)
	mux.Handle(
		agentproto.CancelledPath,
		server.authenticated(http.HandlerFunc(server.cancelled)),
	)
	return noStore(mux)
}
