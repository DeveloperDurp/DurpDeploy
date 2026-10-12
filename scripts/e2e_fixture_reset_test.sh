#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
BASE=http://test.local
ADMIN_EMAIL=e2e-admin@test.local
CSRF=test-token
db_query() { sqlite3 "$TMP/test.db" "$1"; }
source "$SCRIPT_DIR/e2e_fixture_reset.sh"
db_query "
CREATE TABLE users(id INTEGER, email TEXT);
CREATE TABLE projects(id INTEGER, name TEXT, description TEXT, lifecycle_id INTEGER);
CREATE TABLE project_members(project_id INTEGER, user_id INTEGER, role TEXT);
CREATE TABLE lifecycles(id INTEGER, name TEXT, description TEXT);
CREATE TABLE step_templates(id INTEGER, name TEXT);
CREATE TABLE releases(id INTEGER, project_id INTEGER);
CREATE TABLE deployments(id INTEGER, release_id INTEGER, status TEXT);
CREATE TABLE runbook_executions(id INTEGER, deployment_id INTEGER);
INSERT INTO users VALUES(1,'$ADMIN_EMAIL');
INSERT INTO projects VALUES(1,'Customer application','',NULL);
INSERT INTO projects VALUES(2,'TestProject','',NULL);
INSERT INTO project_members VALUES(2,1,'admin');
"
do_delete() {
    printf '%s\n' "$1" >> "$TMP/writes"
    local id=${1##*/}
    case "$1" in
        */projects/*) db_query "DELETE FROM projects WHERE id=$id;" ;;
        */templates/*) db_query "DELETE FROM step_templates WHERE id=$id;" ;;
        *) return 1 ;;
    esac
    printf 303
}
curl_silent() {
    local url=${!#}
    printf '%s\n' "$url" >> "$TMP/writes"
    case "$url" in
        */deployments/3/cancel) db_query "UPDATE deployments SET status='cancelled' WHERE id=3;" ;;
        */lifecycles/4) db_query 'DELETE FROM lifecycles WHERE id=4;' ;;
        *) return 1 ;;
    esac
    printf 303
}
if (reset_e2e_fixtures) > "$TMP/output" 2>&1; then
    echo 'FAIL: reset accepted an unmanaged name collision'; exit 1
fi
[[ ! -e "$TMP/writes" ]]
db_query "UPDATE projects SET description='Managed by make e2e-test.' WHERE id=2;
INSERT INTO projects VALUES(9,'e2e-interpreters-20261005000000-123','',NULL);
INSERT INTO project_members VALUES(9,1,'admin');
INSERT INTO step_templates VALUES(6,'python-template');"
if (reset_e2e_fixtures) > "$TMP/output" 2>&1; then
    echo 'FAIL: reset accepted a pre-existing stable template name'; exit 1
fi
[[ ! -e "$TMP/writes" ]]
db_query 'DELETE FROM projects WHERE id=9; DELETE FROM step_templates;'
db_query "UPDATE projects SET description='Managed by make e2e-test.', lifecycle_id=4 WHERE id=2;
INSERT INTO projects VALUES(5,'e2e-interpreters','Managed by make e2e-test.',NULL);
INSERT INTO project_members VALUES(5,1,'admin');
INSERT INTO lifecycles VALUES(4,'LC','Managed by make e2e-test.');
INSERT INTO step_templates VALUES(6,'python-template');
INSERT INTO step_templates VALUES(7,'Customer template');
INSERT INTO releases VALUES(8,2);
INSERT INTO deployments VALUES(3,8,'queued');
UPDATE projects SET lifecycle_id=4 WHERE id=1;"
if (reset_e2e_fixtures) > "$TMP/output" 2>&1; then
    echo 'FAIL: reset accepted a lifecycle used by an unrelated project'; exit 1
fi
[[ ! -e "$TMP/writes" ]]
db_query 'UPDATE projects SET lifecycle_id=NULL WHERE id=1;'
reset_e2e_fixtures
[[ "$(db_query 'SELECT name FROM projects;')" == 'Customer application' ]]
[[ "$(db_query 'SELECT name FROM step_templates;')" == 'Customer template' ]]
[[ "$(db_query 'SELECT COUNT(*) FROM lifecycles;')" == 0 ]]
[[ "$(db_query 'SELECT status FROM deployments WHERE id=3;')" == cancelled ]]
[[ "$(head -1 "$TMP/writes")" == "$BASE/deployments/3/cancel" ]]
echo 'E2E fixture reset ownership, cancellation, and preservation checks: OK'
