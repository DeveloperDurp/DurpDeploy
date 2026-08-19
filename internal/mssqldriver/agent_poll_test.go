package mssqldriver

import (
	"strings"
	"testing"
)

func TestRewriteSQL_rewritesNestedAgentClaimLimitBeforeOutput(t *testing.T) {
	// Given
	const query = `UPDATE deployment_dispatches
SET state = 'claimed'
WHERE deployment_id = (
    SELECT candidate.deployment_id
    FROM deployment_dispatches AS candidate
    ORDER BY candidate.created_at ASC, candidate.deployment_id ASC
    LIMIT 1
)
RETURNING deployment_id
`
	const want = `UPDATE deployment_dispatches
SET state = 'claimed' OUTPUT INSERTED.deployment_id
WHERE deployment_id = (
    SELECT TOP (1) candidate.deployment_id
    FROM deployment_dispatches AS candidate
    ORDER BY candidate.created_at ASC, candidate.deployment_id ASC
)
`

	// When
	got, err := RewriteSQL(query)

	// Then
	if err != nil {
		t.Fatalf("RewriteSQL() error = %v", err)
	}
	if got != want {
		t.Fatalf("RewriteSQL() = %q, want %q", got, want)
	}
}

func TestRewriteSQL_rewritesAgentClaimSelectors(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name: "direct claim selector",
			query: `SELECT 1
WHERE instr(
                ',' || deployment_dispatches.selector || ',',
                ',' || t.tag_key || '=' || t.tag_value || ','
            ) > 0
`,
			want: `SELECT 1
WHERE CHARINDEX(
                ',' + t.tag_key + '=' + t.tag_value + ',',
                ',' + deployment_dispatches.selector + ','
            ) > 0
`,
		},
		{
			name: "oldest eligible claim selector",
			query: `SELECT 1
WHERE instr(
                    ',' || candidate.selector || ',',
                    ',' || tag.tag_key || '=' || tag.tag_value || ','
                ) > 0
`,
			want: `SELECT 1
WHERE CHARINDEX(
                    ',' + tag.tag_key + '=' + tag.tag_value + ',',
                    ',' + candidate.selector + ','
                ) > 0
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			got, err := RewriteSQL(test.query)

			// Then
			if err != nil {
				t.Fatalf("RewriteSQL() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("RewriteSQL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRewriteSQL_rewritesCompleteAgentPollClaim(t *testing.T) {
	// Given
	const query = `UPDATE deployment_dispatches
SET state = 'claimed'
WHERE deployment_id = (
    SELECT candidate.deployment_id
    FROM deployment_dispatches AS candidate
    JOIN agent_pools AS pool ON pool.id = candidate.pool_id
    JOIN agent_pool_memberships AS membership
        ON membership.pool_id = candidate.pool_id
    JOIN agents AS agent ON agent.id = membership.agent_id
    WHERE candidate.mode = 'remote'
      AND candidate.state = 'waiting'
      AND pool.enabled = 1
      AND (
          candidate.selector = ''
          OR (
              SELECT COUNT(*)
              FROM agent_tags AS tag
              WHERE tag.agent_id = agent.id
                AND instr(
                    ',' || candidate.selector || ',',
                    ',' || tag.tag_key || '=' || tag.tag_value || ','
                ) > 0
          ) = length(candidate.selector) -
              length(replace(candidate.selector, ',', '')) + 1
      )
    ORDER BY candidate.created_at ASC, candidate.deployment_id ASC
    LIMIT 1
)
RETURNING deployment_id
`

	// When
	got, err := RewriteSQL(query)

	// Then
	if err != nil {
		t.Fatalf("RewriteSQL() error = %v", err)
	}
	for _, unsupported := range []string{
		"LIMIT", "instr(", "||", "length(",
	} {
		if strings.Contains(got, unsupported) {
			t.Fatalf(
				"RewriteSQL() retained unsupported %q in %q",
				unsupported,
				got,
			)
		}
	}
	for _, required := range []string{
		"TOP (1)",
		"CHARINDEX(",
		"LEN(candidate.selector)",
		"LEN(replace(candidate.selector, ',', ''))",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf(
				"RewriteSQL() omitted %q in %q",
				required,
				got,
			)
		}
	}
}
