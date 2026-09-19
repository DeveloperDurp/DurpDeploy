package mssqldriver

import (
	"testing"
)

func TestRewriteSQL_ProjectMembershipSource(t *testing.T) {
	query := "SELECT CASE WHEN EXISTS(SELECT 1 FROM project_members " +
		"WHERE project_id = ? AND user_id = ?) THEN 1 ELSE 0 END"
	got, err := RewriteSQL(query)
	want := "SELECT CASE WHEN EXISTS(SELECT 1 FROM project_members " +
		"WHERE project_id = @p1 AND user_id = @p2) THEN 1 ELSE 0 END"
	if err != nil || got != want {
		t.Fatalf("rewritten query=%q error=%v", got, err)
	}
}

func TestRewriteSQL_RemoteAttemptIntegerCasts(t *testing.T) {
	query := "SELECT CAST(? AS INTEGER), ' AS INTEGER' " +
		"FROM deployment_steps s JOIN deployments d " +
		"ON d.id = s.deployment_id WHERE CAST(? AS INTEGER) > 0"
	want := "SELECT CAST(@p1 AS BIGINT), ' AS INTEGER' " +
		"FROM deployment_steps s JOIN deployments d " +
		"ON d.id = s.deployment_id WHERE CAST(@p2 AS BIGINT) > 0"
	got, err := RewriteSQL(query)
	if err != nil || got != want {
		t.Fatalf("attempt casts=%q error=%v", got, err)
	}
}
