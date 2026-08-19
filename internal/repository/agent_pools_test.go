package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestAgentPool_DeleteConflictsWhenPoolIsInUse(t *testing.T) {
	// Given: an enabled pool assigned to a configured environment.
	repo := newTestRepo(t)
	ctx := context.Background()
	poolID := createAgentPool(t, ctx, repo.DB, "production")
	environmentID := createEnvironment(t, ctx, repo.DB, "production")
	upsertEnvironmentPolicy(t, ctx, repo.DB, environmentID, poolID, "[]")

	// When: its hard-delete query is issued.
	_, err := repo.DB.ExecContext(
		ctx,
		agentPoolQuery(t, "DeleteAgentPool"),
		poolID,
	)

	// Then: the restrictive policy reference rejects deletion, while disable remains available.
	if err == nil {
		t.Fatal("DeleteAgentPool() succeeded for a configured pool")
	}
	if _, err := repo.DB.ExecContext(
		ctx,
		agentPoolQuery(t, "DisableAgentPool"),
		poolID,
	); err != nil {
		t.Fatalf("DisableAgentPool(): %v", err)
	}
}

func TestAgentSelector_MatchesExactRequiredTagsAcrossPoolsAndEnvironments(
	t *testing.T,
) {
	// Given: one active agent in two pools, another active agent with a near match,
	// and two configured environments owned by separate projects.
	repo := newTestRepo(t)
	ctx := context.Background()
	poolA := createAgentPool(t, ctx, repo.DB, "pool-a")
	poolB := createAgentPool(t, ctx, repo.DB, "pool-b")
	matchingAgent := createActiveAgent(t, ctx, repo.DB, "matching-agent")
	nearMatchAgent := createActiveAgent(t, ctx, repo.DB, "near-match-agent")
	addAgentToPool(t, ctx, repo.DB, poolA, matchingAgent)
	addAgentToPool(t, ctx, repo.DB, poolB, matchingAgent)
	addAgentToPool(t, ctx, repo.DB, poolA, nearMatchAgent)
	setAgentTag(t, ctx, repo.DB, matchingAgent, "arch", "arm64")
	setAgentTag(t, ctx, repo.DB, matchingAgent, "region", "eu")
	setAgentTag(t, ctx, repo.DB, nearMatchAgent, "arch", "amd64")
	setAgentTag(t, ctx, repo.DB, nearMatchAgent, "region", "eu")
	projectA := createProject(t, ctx, repo.DB, "project-a")
	projectB := createProject(t, ctx, repo.DB, "project-b")
	environmentA := createEnvironment(t, ctx, repo.DB, "environment-a")
	environmentB := createEnvironment(t, ctx, repo.DB, "environment-b")
	requiredTags := `[{"key":"arch","value":"arm64"},{"key":"region","value":"eu"}]`
	upsertEnvironmentPolicy(t, ctx, repo.DB, environmentA, poolA, requiredTags)
	upsertEnvironmentPolicy(t, ctx, repo.DB, environmentB, poolB, requiredTags)
	createDeploymentFor(t, ctx, repo.DB, deploymentTarget{
		projectID: projectA, environmentID: environmentA,
	})
	createDeploymentFor(t, ctx, repo.DB, deploymentTarget{
		projectID: projectB, environmentID: environmentB,
	})

	// When: Go filters SQL pool candidates by the server-owned exact tag superset.
	matchingA := matchingCandidates(
		t,
		ctx,
		repo.DB,
		environmentA,
		map[string]string{
			"arch": "arm64", "region": "eu",
		},
	)
	matchingB := matchingCandidates(
		t,
		ctx,
		repo.DB,
		environmentB,
		map[string]string{
			"arch": "arm64", "region": "eu",
		},
	)

	// Then: the multi-pool agent matches both environments and the near match does not.
	if got, want := matchingA, []string{
		matchingAgent,
	}; !sameStrings(
		got,
		want,
	) {
		t.Fatalf("environment A matches = %v, want %v", got, want)
	}
	if got, want := matchingB, []string{
		matchingAgent,
	}; !sameStrings(
		got,
		want,
	) {
		t.Fatalf("environment B matches = %v, want %v", got, want)
	}

	var selector string
	if err := repo.DB.QueryRowContext(
		ctx,
		agentPoolQuery(t, "GetEnvironmentAgentPolicy"),
		environmentA,
	).Scan(new(int64), new(int64), &selector, new(int64), new(int64)); err != nil {
		t.Fatalf("GetEnvironmentAgentPolicy(): %v", err)
	}
	if selector != requiredTags {
		t.Fatalf(
			"policy selector = %q, want canonical JSON %q",
			selector,
			requiredTags,
		)
	}
}

func TestAgentSelector_DistinguishesConfiguredNoMatchFromUnconfiguredLocal(
	t *testing.T,
) {
	// Given: a configured environment whose only pool member is disabled.
	repo := newTestRepo(t)
	ctx := context.Background()
	poolID := createAgentPool(t, ctx, repo.DB, "disabled-pool")
	disabledAgent := createActiveAgent(t, ctx, repo.DB, "disabled-agent")
	addAgentToPool(t, ctx, repo.DB, poolID, disabledAgent)
	if _, err := repo.DB.ExecContext(
		ctx,
		"UPDATE agents SET status = 'disabled' WHERE id = ?",
		disabledAgent,
	); err != nil {
		t.Fatalf("disable agent: %v", err)
	}
	configuredEnvironment := createEnvironment(t, ctx, repo.DB, "configured")
	unconfiguredEnvironment := createEnvironment(
		t,
		ctx,
		repo.DB,
		"unconfigured",
	)
	upsertEnvironmentPolicy(
		t,
		ctx,
		repo.DB,
		configuredEnvironment,
		poolID,
		"[]",
	)

	// When: candidates and policy are requested for both environments.
	configuredCandidates := candidateIDs(t, ctx, repo.DB, configuredEnvironment)
	var unconfiguredPoolID int64
	err := repo.DB.QueryRowContext(
		ctx,
		agentPoolQuery(t, "GetEnvironmentAgentPolicy"),
		unconfiguredEnvironment,
	).Scan(&unconfiguredPoolID, new(int64), new(string), new(int64), new(int64))

	// Then: a configured no-match is an empty candidate list, not an absent policy.
	if len(configuredCandidates) != 0 {
		t.Fatalf("configured candidates = %v, want none", configuredCandidates)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf(
			"unconfigured policy lookup error = %v, want sql.ErrNoRows",
			err,
		)
	}
}
