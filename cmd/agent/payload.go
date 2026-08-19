package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

type deploymentPayload struct {
	DeploymentID int64              `json:"deployment_id"`
	Release      releasePayload     `json:"release"`
	Environment  environmentPayload `json:"environment"`
	Variables    []variablePayload  `json:"variables"`
}

type releasePayload struct {
	ID        int64         `json:"id"`
	ProjectID int64         `json:"project_id"`
	Version   string        `json:"version"`
	Steps     []stepPayload `json:"steps"`
}

type environmentPayload struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type stepPayload struct {
	Name           string `json:"name"`
	ScriptBody     string `json:"script_body"`
	SortOrder      int64  `json:"sort_order"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
	MaxRetries     int64  `json:"max_retries"`
}

type variablePayload struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

func decodePayload(raw []byte, deploymentID int64) (deploymentPayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload deploymentPayload
	if err := decoder.Decode(&payload); err != nil {
		return deploymentPayload{}, errors.New("invalid deployment payload")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return deploymentPayload{}, errors.New("invalid deployment payload")
	}
	if payload.DeploymentID != deploymentID {
		return deploymentPayload{}, errors.New("deployment payload ID mismatch")
	}
	for index, step := range payload.Release.Steps {
		if step.Name == "" || step.ScriptBody == "" ||
			step.SortOrder != int64(index) ||
			step.TimeoutSeconds < 0 ||
			step.MaxRetries < 0 {
			return deploymentPayload{}, errors.New("invalid deployment payload")
		}
	}
	return payload, nil
}

func (payload deploymentPayload) environment() (map[string]string, []string, error) {
	environment := make(map[string]string, len(payload.Variables))
	secrets := make([]string, 0, len(payload.Variables))
	for _, variable := range payload.Variables {
		if variable.Name == "" {
			return nil, nil, errors.New("invalid deployment variable")
		}
		if _, exists := environment[variable.Name]; exists {
			return nil, nil, fmt.Errorf(
				"duplicate deployment variable %q",
				variable.Name,
			)
		}
		environment[variable.Name] = variable.Value
		if variable.Secret && variable.Value != "" {
			secrets = append(secrets, variable.Value)
		}
	}
	return environment, secrets, nil
}

func (step stepPayload) timeout() time.Duration {
	return time.Duration(step.TimeoutSeconds) * time.Second
}
