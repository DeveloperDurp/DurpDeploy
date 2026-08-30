package handler

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"durpdeploy/internal/agentbootstrap"
	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

type agentPairingBootstrap struct {
	code      agentproto.PairingCode
	endpoint  string
	bootstrap agentpairing.Bootstrap
}

type agentPairingInitiation struct {
	agent   db.Agent
	pairing agentPairingBootstrap
}

func preflightAgentPairing(
	ctx context.Context,
	endpoint string,
	pairingCode string,
	agentFingerprint string,
) (agentPairingBootstrap, bool) {
	code, err := agentproto.ParsePairingCode(strings.TrimSpace(pairingCode))
	if err != nil {
		return agentPairingBootstrap{}, false
	}
	agentPin, err := agentproto.ParseSHA256Pin(
		strings.TrimSpace(agentFingerprint),
	)
	if err != nil {
		return agentPairingBootstrap{}, false
	}
	return agentPairingBootstrap{
		code: code, endpoint: endpoint,
		bootstrap: agentpairing.Bootstrap{AgentPin: agentPin},
	}, true
}

func (h *AgentAdminHandler) NewAgentForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := pages.AgentFormPage("", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"could not render agent form",
			http.StatusInternalServerError,
		)
	}
}

func (h *AgentAdminHandler) CreateAgentForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	if h.pairer == nil {
		http.Error(
			w,
			"agent pairing is unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid agent form", http.StatusBadRequest)
		return
	}
	request := agentAdminRequest{
		ID:           uuid.NewString(),
		Name:         strings.TrimSpace(r.FormValue("name")),
		AgentVersion: strings.TrimSpace(r.FormValue("agent_version")),
	}
	if !validAgentText(request.Name) {
		http.Error(
			w,
			"name is required",
			http.StatusUnprocessableEntity,
		)
		return
	}
	endpoint, err := normalizeAgentEndpoint(
		r.FormValue("agent_host"),
		r.FormValue("agent_port"),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	pairing, ok := preflightAgentPairing(
		r.Context(),
		endpoint,
		r.FormValue("pairing_code"),
		r.FormValue("agent_fingerprint"),
	)
	if !ok {
		form := pages.AgentFormPage(
			"agent pairing offer is invalid",
			r.URL.Path,
		)
		WriteFormError(w, r, form, form)
		return
	}
	initiation := agentPairingInitiation{
		agent: db.Agent{
			ID: request.ID, Name: request.Name, Status: "pending",
			AgentVersion: sql.NullString{
				String: request.AgentVersion, Valid: request.AgentVersion != "",
			},
		},
		pairing: pairing,
	}
	confirmation, err := renderAgentPairingConfirmation(
		r.Context(),
		r.URL.Path,
		initiation,
	)
	if err != nil {
		http.Error(
			w,
			"could not render pairing confirmation",
			http.StatusInternalServerError,
		)
		return
	}
	_, err = h.repo.Queries.CreatePendingAgent(
		r.Context(),
		db.CreatePendingAgentParams{
			ID: request.ID, Name: request.Name,
			AgentVersion: initiation.agent.AgentVersion,
		},
	)
	if IsUniqueViolation(err) {
		http.Error(w, "agent id already exists", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "could not create agent", http.StatusInternalServerError)
		return
	}
	h.finishPairing(w, initiation, confirmation)
}

func normalizeAgentEndpoint(host, port string) (string, error) {
	const invalidEndpoint = "agent endpoint must be a hostname or IP address with an optional port"

	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" || strings.IndexFunc(host, unicode.IsSpace) >= 0 {
		return "", errors.New(invalidEndpoint)
	}

	addressHost := host
	if strings.HasPrefix(host, "[") || strings.HasSuffix(host, "]") {
		if len(host) < 3 || host[0] != '[' || host[len(host)-1] != ']' {
			return "", errors.New(invalidEndpoint)
		}
		addressHost = host[1 : len(host)-1]
		address, err := netip.ParseAddr(addressHost)
		if err != nil || !address.Is6() || address.Zone() != "" {
			return "", errors.New(invalidEndpoint)
		}
	} else if address, err := netip.ParseAddr(host); err == nil {
		if !address.Is4() {
			return "", errors.New(invalidEndpoint)
		}
	} else {
		if len(host) > 253 || strings.HasSuffix(host, ".") {
			return "", errors.New(invalidEndpoint)
		}
		numericAddress := true
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' ||
				label[len(label)-1] == '-' {
				return "", errors.New(invalidEndpoint)
			}
			for _, character := range label {
				if character != '-' && (character < '0' || character > '9') &&
					(character < 'a' || character > 'z') &&
					(character < 'A' || character > 'Z') {
					return "", errors.New(invalidEndpoint)
				}
				if character < '0' || character > '9' {
					numericAddress = false
				}
			}
		}
		if numericAddress && strings.Contains(host, ".") {
			return "", errors.New(invalidEndpoint)
		}
	}

	endpoint := "https://" + host
	if port == "" {
		_, defaultPort, err := net.SplitHostPort(
			agentbootstrap.DefaultListenAddr,
		)
		if err != nil {
			return "", errors.New(invalidEndpoint)
		}
		port = defaultPort
	}
	for _, character := range port {
		if character < '0' || character > '9' {
			return "", errors.New(invalidEndpoint)
		}
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return "", errors.New(invalidEndpoint)
	}
	if portNumber == 443 {
		return endpoint, nil
	}
	return "https://" + net.JoinHostPort(
		addressHost,
		strconv.FormatUint(portNumber, 10),
	), nil
}
