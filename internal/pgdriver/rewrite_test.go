package pgdriver

import "testing"

func TestRewriteSQL(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "autoincrement pk",
			query: "CREATE TABLE projects (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)",
			want:  "CREATE TABLE projects (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL)",
		},
		{
			name:  "plain integer column",
			query: "project_id INTEGER NOT NULL REFERENCES projects(id)",
			want:  "project_id BIGINT NOT NULL REFERENCES projects(id)",
		},
		{
			name:  "binary column",
			query: "encrypted_seed BLOB NOT NULL",
			want:  "encrypted_seed BYTEA NOT NULL",
		},
		{
			name:  "unixepoch default",
			query: "created_at INTEGER NOT NULL DEFAULT (unixepoch())",
			want:  "created_at BIGINT NOT NULL DEFAULT (extract(epoch from now())::bigint)",
		},
		{
			name:  "strftime start of day",
			query: "SELECT COUNT(*) FROM deployments WHERE created_at >= strftime('%s','now','start of day')",
			want:  "SELECT COUNT(*) FROM deployments WHERE created_at >= extract(epoch from date_trunc('day', now()))::bigint",
		},
		{
			name:  "instr predicate",
			query: "SELECT instr(selector, ?) > 0",
			want:  "SELECT strpos(selector, $1) > 0",
		},
		{
			name:  "placeholders renumbered",
			query: "INSERT INTO users (email, password_hash) VALUES (?, ?)",
			want:  "INSERT INTO users (email, password_hash) VALUES ($1, $2)",
		},
		{
			name:  "placeholder inside string literal untouched",
			query: "SELECT 1 WHERE name = 'a?b' AND id = ?",
			want:  "SELECT 1 WHERE name = 'a?b' AND id = $1",
		},

		{
			name:  "numbered placeholders preserved",
			query: "SELECT ?1, ?1, ?2, ?7, ?",
			want:  "SELECT $1, $1, $2, $7, $8",
		},
		{
			name:  "numbered placeholder inside string literal untouched",
			query: "SELECT '?1' AS literal, ?1 AS arg",
			want:  "SELECT '?1' AS literal, $1 AS arg",
		},
		{
			name:  "no-op when nothing to rewrite",
			query: "SELECT * FROM projects",
			want:  "SELECT * FROM projects",
		},
		{
			name:  "idempotent on already-rewritten SQL",
			query: "id BIGSERIAL PRIMARY KEY, created_at BIGINT DEFAULT (extract(epoch from now())::bigint)",
			want:  "id BIGSERIAL PRIMARY KEY, created_at BIGINT DEFAULT (extract(epoch from now())::bigint)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RewriteSQL(tc.query)
			if got != tc.want {
				t.Errorf("RewriteSQL(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
