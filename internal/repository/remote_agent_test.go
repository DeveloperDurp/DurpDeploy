package repository_test

import (
	"context"
	"testing"
)

func TestAgentMissingQuery(t *testing.T) {
	r := newTestRepo(t)
	rows, err := r.Queries.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("fresh agents=%d", len(rows))
	}
}
