package main

import "testing"

func TestDecodePayload_rejectsDeploymentMismatch(t *testing.T) {
	// Given
	raw := []byte(
		`{"deployment_id":41,"release":{"id":1,"project_id":1,"version":"v1","steps":[]},"environment":{"id":1,"name":"test"},"variables":[]}`,
	)

	// When
	_, err := decodePayload(raw, 42)

	// Then
	if err == nil {
		t.Fatal("mismatched payload accepted")
	}
}
