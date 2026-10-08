#!/usr/bin/env bash
# Sourced by the running-server suite. Only its named fixtures are replaced.
reset_e2e_fixtures() {
    local id name code dep execution state attempt project_name
    local projects="$TMP/previous-projects"
    local lifecycles="$TMP/previous-lifecycles"
    local templates="$TMP/previous-templates"
    local admin_id
    admin_id=$(db_query "SELECT id FROM users WHERE email='$ADMIN_EMAIL';")
    # Names from older runs are recognized exactly, not with broad prefixes.
    db_query 'SELECT name FROM projects;' | python3 -c '
import re,sys
stable={"TestProject","LC-Project","AppProject","DP-cross-proj","e2e-api-project","e2e-interpreters","e2e-verify","terraform-approval","stage-handoff","list-access-owned","list-access-foreign"}
legacy=r"(?:(?:TestProject|LC-Project|AppProject|DP-cross-proj|e2e-api-project|e2e-interpreters|e2e-verify|terraform-approval)-[0-9]{14}-[0-9]+|stage-handoff-[0-9]+-[0-9]+|list-access-[0-9]+-[0-9]+-(?:owned|foreign))"
for line in sys.stdin:
    name=line.strip()
    if name in stable or re.fullmatch(legacy,name): print(name)
' > "$projects"
    db_query 'SELECT name FROM lifecycles;' | python3 -c '
import re,sys
for line in sys.stdin:
    name=line.strip()
    if name in {"LC","app-lifecycle"} or re.fullmatch(r"(?:LC|app-lifecycle)-[0-9]{14}-[0-9]+",name): print(name)
' > "$lifecycles"
    db_query 'SELECT name FROM step_templates;' | python3 -c '
import re,sys
for line in sys.stdin:
    name=line.strip()
    if name in {"python-template","python-step"} or re.fullmatch(r"python-(?:template|step)-[0-9]{14}-[0-9]+",name): print(name)
' > "$templates"
    # Check ownership before changing anything. Stable names need our marker.
    while IFS= read -r name; do
        id=$(db_query "SELECT p.id FROM projects p JOIN project_members m ON m.project_id=p.id WHERE p.name='$name' AND m.user_id=$admin_id AND m.role='admin' AND (p.description LIKE '%Managed by make e2e-test.%' OR p.name NOT IN ('TestProject','LC-Project','AppProject','DP-cross-proj','e2e-api-project','e2e-interpreters','e2e-verify','terraform-approval','stage-handoff','list-access-owned','list-access-foreign'));")
        [[ -n "$id" ]] || { echo "FAIL: refusing to replace unmanaged project $name" >&2; return 1; }
    done < "$projects"
    while IFS= read -r name; do
        id=$(db_query "SELECT id FROM lifecycles WHERE name='$name' AND (description LIKE '%Managed by make e2e-test.%' OR name NOT IN ('LC','app-lifecycle'));")
        [[ -n "$id" ]] || { echo "FAIL: refusing to replace unmanaged lifecycle $name" >&2; return 1; }
        while IFS= read -r project_name; do
            [[ -z "$project_name" ]] || grep -Fxq "$project_name" "$projects" || {
                echo "FAIL: lifecycle $name is used by an unmanaged project" >&2; return 1;
            }
        done < <(db_query "SELECT name FROM projects WHERE lifecycle_id=$id;")
    done < "$lifecycles"
    if [[ -s "$templates" ]] && ! grep -q '^e2e-interpreters\(-[0-9]\{14\}-[0-9]\+\)\?$' "$projects"; then
        echo 'FAIL: refusing to replace templates without their E2E project' >&2; return 1
    fi
    if grep -Eq '^python-(template|step)$' "$templates" && ! grep -Fxq e2e-interpreters "$projects"; then
        echo 'FAIL: stable template names already exist without their managed project' >&2; return 1
    fi
    while IFS= read -r name; do
        id=$(db_query "SELECT id FROM projects WHERE name='$name';")
        # Cancel through the public web routes; never delete executing work.
        for dep in $(db_query "SELECT d.id FROM deployments d JOIN releases r ON r.id=d.release_id WHERE r.project_id=$id AND d.status NOT IN ('succeeded','failed','cancelled','rejected');"); do
            execution=$(db_query "SELECT id FROM runbook_executions WHERE deployment_id=$dep;")
            if [[ -n "$execution" ]]; then
                code=$(curl_silent -X POST -d "csrf_token=$CSRF" "$BASE/projects/$id/runbooks/executions/$execution/cancel")
            else
                code=$(curl_silent -X POST -d "csrf_token=$CSRF" "$BASE/deployments/$dep/cancel")
            fi
            [[ "$code" == 303 ]] || { echo "FAIL: cancel previous E2E job $dep got $code" >&2; return 1; }
            for attempt in {1..300}; do
                state=$(db_query "SELECT status FROM deployments WHERE id=$dep;")
                [[ "$state" =~ ^(succeeded|failed|cancelled|rejected)$ ]] && break
                sleep 0.2
            done
            [[ "$state" =~ ^(succeeded|failed|cancelled|rejected)$ ]] || { echo "FAIL: previous E2E job $dep still $state" >&2; return 1; }
        done
        code=$(do_delete "$BASE/projects/$id")
        [[ "$code" == 303 ]] || { echo "FAIL: reset project $name got $code" >&2; return 1; }
    done < "$projects"
    while IFS= read -r name; do
        id=$(db_query "SELECT id FROM step_templates WHERE name='$name';")
        code=$(do_delete "$BASE/templates/$id")
        [[ "$code" == 303 ]] || { echo "FAIL: reset template $name got $code" >&2; return 1; }
    done < "$templates"
    while IFS= read -r name; do
        id=$(db_query "SELECT id FROM lifecycles WHERE name='$name';")
        code=$(curl_silent -X POST -d "_method=delete&csrf_token=$CSRF" "$BASE/lifecycles/$id")
        [[ "$code" == 303 ]] || { echo "FAIL: reset lifecycle $name got $code" >&2; return 1; }
    done < "$lifecycles"
    echo '  Previous E2E fixtures replaced; shared environments preserved: OK'
}
