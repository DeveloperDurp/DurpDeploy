package mssqldriver

import (
	"strings"
	"testing"
)

func TestRewriteSQL(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		want    string
		wantErr bool
	}{
		{
			name:  "renumbers source placeholders in argument order",
			query: "UPDATE projects SET lifecycle_id = ? WHERE id = ?;",
			want:  "UPDATE projects SET lifecycle_id = @p1 WHERE id = @p2;",
		},
		{
			name: "preserves repeated explicit deployment count ordinals",
			query: "SELECT COUNT(*)\n" +
				"FROM deployments d\n" +
				"WHERE (CAST(?1 AS INTEGER) IS NULL OR d.release_id IN (SELECT id FROM releases WHERE project_id = CAST(?1 AS INTEGER)))\n" +
				"  AND (CAST(?2 AS INTEGER) IS NULL OR d.environment_id = CAST(?2 AS INTEGER))\n",
			want: "SELECT COUNT(*)\n" +
				"FROM deployments d\n" +
				"WHERE (CAST(@p1 AS BIGINT) IS NULL OR d.release_id IN (SELECT id FROM releases WHERE project_id = CAST(@p1 AS BIGINT)))\n" +
				"  AND (CAST(@p2 AS BIGINT) IS NULL OR d.environment_id = CAST(@p2 AS BIGINT))\n",
		},
		{
			name: "preserves explicit deployment filter pagination ordinals",
			query: "SELECT d.id\n" +
				"FROM deployments d\n" +
				"WHERE (CAST(?1 AS INTEGER) IS NULL OR d.release_id IN (SELECT id FROM releases WHERE project_id = CAST(?1 AS INTEGER)))\n" +
				"  AND (CAST(?2 AS INTEGER) IS NULL OR d.environment_id = CAST(?2 AS INTEGER))\n" +
				"  AND (CAST(?3 AS TEXT) IS NULL OR d.status = CAST(?3 AS TEXT))\n" +
				"  AND (CAST(?4 AS INTEGER) IS NULL OR d.created_at >= CAST(?4 AS INTEGER))\n" +
				"  AND (CAST(?5 AS INTEGER) IS NULL OR d.created_at <= CAST(?5 AS INTEGER))\n" +
				"ORDER BY d.created_at DESC\n" +
				"LIMIT ?7 OFFSET ?6\n",
			want: "SELECT d.id\n" +
				"FROM deployments d\n" +
				"WHERE (CAST(@p1 AS BIGINT) IS NULL OR d.release_id IN (SELECT id FROM releases WHERE project_id = CAST(@p1 AS BIGINT)))\n" +
				"  AND (CAST(@p2 AS BIGINT) IS NULL OR d.environment_id = CAST(@p2 AS BIGINT))\n" +
				"  AND (CAST(@p3 AS NVARCHAR(MAX)) IS NULL OR d.status = CAST(@p3 AS NVARCHAR(MAX)))\n" +
				"  AND (CAST(@p4 AS BIGINT) IS NULL OR d.created_at >= CAST(@p4 AS BIGINT))\n" +
				"  AND (CAST(@p5 AS BIGINT) IS NULL OR d.created_at <= CAST(@p5 AS BIGINT))\n" +
				"ORDER BY d.created_at DESC\n" +
				"OFFSET @p6 ROWS FETCH NEXT @p7 ROWS ONLY\n",
		},
		{
			name:  "leaves quoted question marks untouched",
			query: `SELECT "?" FROM deployments WHERE status = '?' AND id = ?;`,
			want:  `SELECT "?" FROM deployments WHERE status = '?' AND id = @p1;`,
		},
		{
			name:  "leaves quoted as text unchanged",
			query: `SELECT ' AS TEXT', CAST(? AS TEXT);`,
			want:  `SELECT ' AS TEXT', CAST(@p1 AS NVARCHAR(MAX));`,
		},
		{
			name:  "leaves quoted returning unchanged",
			query: `SELECT 'RETURNING' AS label;`,
			want:  `SELECT 'RETURNING' AS label;`,
		},
		{
			name:    "rejects update returning when where is only quoted",
			query:   "UPDATE projects SET description = 'WHERE' RETURNING id;",
			wantErr: true,
		},
		{
			name: "widens audit log filter integer casts",
			query: "SELECT id FROM audit_log\n" +
				"WHERE (CAST(?1 AS INTEGER) IS NULL OR user_id = CAST(?1 AS INTEGER))\n" +
				"  AND (CAST(?2 AS TEXT) IS NULL OR action = CAST(?2 AS TEXT))\n" +
				"LIMIT ?4\n",
			want: "SELECT TOP (@p4) id FROM audit_log\n" +
				"WHERE (CAST(@p1 AS BIGINT) IS NULL OR user_id = CAST(@p1 AS BIGINT))\n" +
				"  AND (CAST(@p2 AS NVARCHAR(MAX)) IS NULL OR action = CAST(@p2 AS NVARCHAR(MAX)))\n",
		},
		{
			name:  "moves insert returning to output",
			query: "INSERT INTO projects (name, description) VALUES (?, ?) RETURNING *;",
			want: "INSERT INTO projects (name, description) OUTPUT INSERTED.* " +
				"VALUES (@p1, @p2);",
		},
		{
			name:  "rewrites real AS TEXT while preserving quoted AS TEXT and escaped apostrophe",
			query: "UPDATE projects SET status = CAST(? AS TEXT), notes = 'it''s text: AS TEXT in a literal and RETURNING too' WHERE id = ?;",
			want:  "UPDATE projects SET status = CAST(@p1 AS NVARCHAR(MAX)), notes = 'it''s text: AS TEXT in a literal and RETURNING too' WHERE id = @p2;",
		},
		{
			name: "moves multiline returning star to output",
			query: "-- name: CreateNotificationEvent :one\n" +
				"INSERT INTO notification_events (event_type, deployment_id, project_id, environment_id, message, results)\n" +
				"VALUES (?, ?, ?, ?, ?, ?)\n" +
				"RETURNING *;",
			want: "-- name: CreateNotificationEvent :one\n" +
				"INSERT INTO notification_events (event_type, deployment_id, project_id, environment_id, message, results) OUTPUT INSERTED.*\n" +
				"VALUES (@p1, @p2, @p3, @p4, @p5, @p6);",
		},
		{
			name: "handles mixed case multiline values",
			query: "-- name: CreateNotificationEvent :one\n" +
				"INSERT INTO notification_events (event_type, deployment_id, project_id, environment_id, message, results)\n" +
				"VaLuEs (?, ?, ?, ?, ?, ?)\n" +
				"RETURNING id, event_type, deployment_id, project_id, environment_id, message, results, created_at\n",
			want: "-- name: CreateNotificationEvent :one\n" +
				"INSERT INTO notification_events (event_type, deployment_id, project_id, environment_id, message, results) OUTPUT INSERTED.id, INSERTED.event_type, INSERTED.deployment_id, INSERTED.project_id, INSERTED.environment_id, INSERTED.message, INSERTED.results, INSERTED.created_at\n" +
				"VaLuEs (@p1, @p2, @p3, @p4, @p5, @p6)\n",
		},
		{
			name:  "moves update returning to output",
			query: "UPDATE projects SET name = ?, description = ? WHERE id = ? RETURNING *;",
			want: "UPDATE projects SET name = @p1, description = @p2 OUTPUT INSERTED.* " +
				"WHERE id = @p3;",
		},
		{
			name:  "does not rewrite quoted WHERE/RETURNING/AS TEXT when moving update RETURNING output",
			query: "UPDATE projects SET status = CAST(? AS TEXT), notes = 'it''s a SQL literal with RETURNING, WHERE, and AS TEXT' WHERE id = ? RETURNING *;",
			want:  "UPDATE projects SET status = CAST(@p1 AS NVARCHAR(MAX)), notes = 'it''s a SQL literal with RETURNING, WHERE, and AS TEXT' OUTPUT INSERTED.* WHERE id = @p2;",
		},
		{
			name:  "moves update returning when quoted literal resembles insert",
			query: "UPDATE projects SET notes = 'INSERT INTO' WHERE id = ? RETURNING id;",
			want:  "UPDATE projects SET notes = 'INSERT INTO' OUTPUT INSERTED.id WHERE id = @p1;",
		},
		{
			name: "moves semicolon-less global notification returning to output",
			query: "UPDATE global_notifications\n" +
				"SET slack_webhook_url = ?, notify_emails = ?, gotify_url = ?, gotify_token = ?, discord_webhook_url = ?, updated_at = unixepoch()\n" +
				"WHERE id = 1\n" +
				"RETURNING id, slack_webhook_url, notify_emails, gotify_url, gotify_token, discord_webhook_url, updated_at\n",
			want: "UPDATE global_notifications\n" +
				"SET slack_webhook_url = @p1, notify_emails = @p2, gotify_url = @p3, gotify_token = @p4, discord_webhook_url = @p5, updated_at = " +
				"DATEDIFF_BIG(SECOND, '1970-01-01T00:00:00Z', SYSUTCDATETIME()) OUTPUT INSERTED.id, INSERTED.slack_webhook_url, INSERTED.notify_emails, INSERTED.gotify_url, INSERTED.gotify_token, INSERTED.discord_webhook_url, INSERTED.updated_at\n" +
				"WHERE id = 1\n",
		},
		{
			name:  "rewrites limit only to top",
			query: "SELECT * FROM audit_log ORDER BY created_at DESC, id DESC LIMIT ?;",
			want:  "SELECT TOP (@p1) * FROM audit_log ORDER BY created_at DESC, id DESC;",
		},
		{
			name: "rewrites literal approval limit to top",
			query: "SELECT id, deployment_id, approved_by, approved_at, approver_user_id, required_approver_role " +
				"FROM deployment_approvals WHERE deployment_id = ? ORDER BY approved_at ASC LIMIT 1\n",
			want: "SELECT TOP (1) id, deployment_id, approved_by, approved_at, approver_user_id, required_approver_role " +
				"FROM deployment_approvals WHERE deployment_id = @p1 ORDER BY approved_at ASC\n",
		},
		{
			name: "rewrites multiline notification limit to top",
			query: "SELECT\n" +
				"    ne.id, ne.event_type\n" +
				"FROM notification_events ne\n" +
				"ORDER BY ne.created_at DESC\n" +
				"LIMIT ?\n",
			want: "SELECT TOP (@p1)\n" +
				"    ne.id, ne.event_type\n" +
				"FROM notification_events ne\n" +
				"ORDER BY ne.created_at DESC\n",
		},
		{
			name: "rewrites limit offset without reordering bindings",
			query: "SELECT p.* FROM projects p\n" +
				"JOIN project_members pm ON pm.project_id = p.id\n" +
				"WHERE pm.user_id = ?\n" +
				"ORDER BY p.created_at DESC\n" +
				"LIMIT ? OFFSET ?;",
			want: "SELECT p.* FROM projects p\n" +
				"JOIN project_members pm ON pm.project_id = p.id\n" +
				"WHERE pm.user_id = @p1\n" +
				"ORDER BY p.created_at DESC\n" +
				"OFFSET @p3 ROWS FETCH NEXT @p2 ROWS ONLY;",
		},
		{
			name:  "rewrites unixepoch using utc",
			query: "UPDATE users SET name = ?, role = ?, updated_at = unixepoch() WHERE id = ?;",
			want: "UPDATE users SET name = @p1, role = @p2, updated_at = " +
				"DATEDIFF_BIG(SECOND, '1970-01-01T00:00:00Z', SYSUTCDATETIME()) " +
				"WHERE id = @p3;",
		},
		{
			name: "rewrites strftime now using utc",
			query: "UPDATE api_tokens SET revoked_at = strftime('%s','now') " +
				"WHERE id = ? AND revoked_at IS NULL;",
			want: "UPDATE api_tokens SET revoked_at = " +
				"DATEDIFF_BIG(SECOND, '1970-01-01T00:00:00Z', SYSUTCDATETIME()) " +
				"WHERE id = @p1 AND revoked_at IS NULL;",
		},
		{
			name: "rewrites start of day using utc",
			query: "SELECT COUNT(*) FROM deployments WHERE created_at >= " +
				"strftime('%s','now','start of day');",
			want: "SELECT COUNT(*) FROM deployments WHERE created_at >= " +
				"DATEDIFF_BIG(SECOND, '1970-01-01T00:00:00Z', " +
				"CONVERT(datetime2, CONVERT(date, SYSUTCDATETIME())));",
		},
		{
			name: "rewrites project member conflict to locked merge",
			query: "INSERT INTO project_members (project_id, user_id, role) VALUES (?, ?, ?)\n" +
				"ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role;",
			want: "MERGE project_members WITH (HOLDLOCK) AS target\n" +
				"USING (VALUES (@p1, @p2, @p3)) AS source (project_id, user_id, role)\n" +
				"ON target.project_id = source.project_id AND target.user_id = source.user_id\n" +
				"WHEN MATCHED THEN UPDATE SET role = source.role\n" +
				"WHEN NOT MATCHED THEN INSERT (project_id, user_id, role)\n" +
				"VALUES (source.project_id, source.user_id, source.role);",
		},
		{
			name: "terminates semicolon-less project member conflict merge",
			query: "INSERT INTO project_members (project_id, user_id, role) VALUES (?, ?, ?)\n" +
				"ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role",
			want: "MERGE project_members WITH (HOLDLOCK) AS target\n" +
				"USING (VALUES (@p1, @p2, @p3)) AS source (project_id, user_id, role)\n" +
				"ON target.project_id = source.project_id AND target.user_id = source.user_id\n" +
				"WHEN MATCHED THEN UPDATE SET role = source.role\n" +
				"WHEN NOT MATCHED THEN INSERT (project_id, user_id, role)\n" +
				"VALUES (source.project_id, source.user_id, source.role);",
		},
		{
			name: "rewrites environment policy conflict to locked merge",
			query: "INSERT INTO environment_agent_policies (environment_id, pool_id, selector) VALUES (?, ?, ?)\n" +
				"ON CONFLICT (environment_id) DO UPDATE SET pool_id = excluded.pool_id, selector = excluded.selector",
			want: "MERGE environment_agent_policies WITH (HOLDLOCK) AS target\n" +
				"USING (VALUES (@p1, @p2, @p3)) AS source (environment_id, pool_id, selector)\n" +
				"ON target.environment_id = source.environment_id\n" +
				"WHEN MATCHED THEN UPDATE SET pool_id = source.pool_id, selector = source.selector\n" +
				"WHEN NOT MATCHED THEN INSERT (environment_id, pool_id, selector)\n" +
				"VALUES (source.environment_id, source.pool_id, source.selector);",
		},
		{
			name: "rewrites project membership exists to integer scalar",
			query: "-- name: IsProjectMember :one\n" +
				"SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id = ? AND user_id = ?)\n",
			want: "-- name: IsProjectMember :one\n" +
				"SELECT CASE WHEN EXISTS(SELECT 1 FROM project_members WHERE project_id = @p1 AND user_id = @p2) " +
				"THEN CAST(1 AS BIGINT) ELSE CAST(0 AS BIGINT) END\n",
		},
		{
			name:    "rejects malformed returning clause",
			query:   "INSERT INTO projects (name) VALUES (?) RETURNING;",
			wantErr: true,
		},
		{
			name: "rejects unsupported conflict target",
			query: "INSERT INTO project_members (project_id, user_id, role) VALUES (?, ?, ?)\n" +
				"ON CONFLICT (user_id) DO UPDATE SET role = excluded.role;",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When
			got, err := RewriteSQL(tc.query)

			// Then
			if tc.wantErr {
				if err == nil {
					t.Fatalf(
						"RewriteSQL(%q) error = nil, want an error",
						tc.query,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("RewriteSQL(%q) error = %v, want nil", tc.query, err)
			}
			if strings.Contains(tc.name, "project member conflict") &&
				!strings.HasSuffix(got, ";") {
				t.Fatalf(
					"RewriteSQL(%q) = %q, want trailing semicolon",
					tc.query,
					got,
				)
			}
			if got != tc.want {
				t.Errorf("RewriteSQL(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
