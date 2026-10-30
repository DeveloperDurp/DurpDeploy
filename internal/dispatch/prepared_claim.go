package dispatch

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"slices"
	"sort"
	"strings"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func prepareRemoteClaim(
	snapshot repository.RemotePayloadSnapshot,
) (repository.RemotePreparedClaim, error) {
	if snapshot.Artifact != nil &&
		(!snapshot.Staging || len(snapshot.Steps) != 1) {
		return repository.RemotePreparedClaim{}, agentproto.ErrUnsupportedProtocol
	}
	resolved, err := runner.ResolveDeploymentVariables(
		snapshot.Variables,
		snapshot.Deployment,
	)
	if err != nil {
		return repository.RemotePreparedClaim{}, err
	}
	resolved, err = selectRemoteVariables(snapshot.Steps, resolved)
	if err != nil {
		return repository.RemotePreparedClaim{}, err
	}
	for _, variable := range resolved {
		if err := containerenv.ValidateEntry(variable.Name, variable.Value); err != nil {
			return repository.RemotePreparedClaim{}, err
		}
	}
	steps := make([]executor.Step, len(snapshot.Steps))
	protocol := agentproto.AgentV1
	if snapshot.Agent.AgentProtocol.Valid {
		protocol, err = agentproto.ParseProtocolVersion(
			snapshot.Agent.AgentProtocol.String,
		)
		if err != nil {
			return repository.RemotePreparedClaim{}, err
		}
	}
	for index, step := range snapshot.Steps {
		names, err := remoteClaimVariableNames(step, resolved, protocol)
		if err != nil {
			return repository.RemotePreparedClaim{}, err
		}
		steps[index] = executor.Step{
			Name:           step.Name,
			ScriptBody:     step.ScriptBody,
			Interpreter:    executor.Interpreter(step.Interpreter),
			SortOrder:      int64(index + 1),
			TimeoutSeconds: step.TimeoutSeconds,
			MaxRetries:     step.MaxRetries,
			ExecutionMode:  agentproto.ExecutionMode(step.AgentExecutionMode),
			ContainerImage: step.ContainerImage,
			VariableNames:  names,
		}
	}
	payload := Payload{
		DeploymentID: agentproto.DeploymentID(snapshot.Deployment.ID),
		Release: ReleaseSnapshot{
			Artifact:  snapshot.Artifact,
			Staging:   snapshot.Staging,
			ID:        snapshot.Release.ID,
			ProjectID: snapshot.Release.ProjectID,
			Version:   snapshot.Release.Version,
			Steps:     steps,
		},
		Environment: EnvironmentSnapshot{
			ID:   snapshot.Deployment.EnvironmentID,
			Name: snapshot.Deployment.EnvironmentNameSnapshot.String,
		},
		Variables: VariableSnapshots(resolved),
	}
	certificate, _ := pem.Decode([]byte(snapshot.Agent.CertificatePem.String))
	if certificate == nil || certificate.Type != "CERTIFICATE" {
		return repository.RemotePreparedClaim{}, fmt.Errorf(
			"decode agent certificate",
		)
	}
	ciphertext, err := payload.SealForProtocol(certificate.Bytes, protocol)
	if err != nil {
		return repository.RemotePreparedClaim{}, err
	}
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return repository.RemotePreparedClaim{}, fmt.Errorf(
			"generate claim token: %w",
			err,
		)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	tokenHash := sha256.Sum256([]byte(token))
	return repository.RemotePreparedClaim{
		Token: token, TokenHash: tokenHash[:], Ciphertext: ciphertext,
	}, nil
}

func remoteClaimVariableNames(
	step db.DeploymentStep,
	resolved []runner.ResolvedVariable,
	protocol agentproto.ProtocolVersion,
) ([]string, error) {
	var names []string
	if step.VariableNames != "" {
		if err := json.Unmarshal(
			[]byte(step.VariableNames),
			&names,
		); err != nil {
			return nil, fmt.Errorf(
				"decode step variables: %w",
				err,
			)
		}
	}
	if len(names) != 0 {
		for _, variable := range resolved {
			if containerenv.IsEnvironmentIdentity(variable.Name) &&
				!slices.Contains(names, variable.Name) {
				names = append(names, variable.Name)
			}
		}
	}
	if protocol != agentproto.AgentV3 && len(names) != 0 {
		selected, err := selectRemoteVariables(
			[]db.DeploymentStep{step},
			resolved,
		)
		if err != nil {
			return nil, err
		}
		if !slices.Equal(selected, resolved) {
			return nil, agentproto.ErrUnsupportedProtocol
		}
		// Every legacy step must receive the same selected variables.
		names = nil
	}
	return names, nil
}

func selectRemoteVariables(
	steps []db.DeploymentStep,
	variables []runner.ResolvedVariable,
) ([]runner.ResolvedVariable, error) {
	selected := make(map[string]struct{})
	for _, step := range steps {
		if step.VariableNames == "" {
			return variables, nil
		}
		var names []string
		if err := json.Unmarshal(
			[]byte(step.VariableNames),
			&names,
		); err != nil {
			return nil, fmt.Errorf(
				"decode variable allowlist for step %q: %w",
				step.Name,
				err,
			)
		}
		if len(names) == 0 {
			return variables, nil
		}
		for _, name := range names {
			selected[name] = struct{}{}
		}
	}
	result := make([]runner.ResolvedVariable, 0, len(selected))
	for _, variable := range variables {
		if _, ok := selected[variable.Name]; ok ||
			containerenv.IsEnvironmentIdentity(variable.Name) {
			result = append(result, variable)
			delete(selected, variable.Name)
		}
	}
	if len(selected) != 0 {
		missing := make([]string, 0, len(selected))
		for name := range selected {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf(
			"selected remote variables are unavailable: %s",
			strings.Join(missing, ", "),
		)
	}
	return result, nil
}
