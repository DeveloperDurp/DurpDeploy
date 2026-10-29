# Rotate a GitLab project token with a runbook

Use this guide to set up a runbook once and start it whenever your GitLab project token needs replacement.
The runbook creates a new token and saves it in DurpDeploy.
You then check a deployment and revoke the old token in GitLab.

The old token stays active until you revoke it.
This gives you time to check the replacement before other users or deployments lose access.
GitLab's **Rotate** action disables the old token immediately.

## Before you start

This example uses two DurpDeploy projects:

- **Application project:** the project whose deployments use `GITLAB_PROJECT_TOKEN`.
- **Rotation project:** a separate project that stores the runbook and its two management tokens.

The example uses a credential available to **All environments**.
Existing deployments must already use the variable name `GITLAB_PROJECT_TOKEN`.
Other names or environment-specific credentials require changes to the example script.

1. Confirm that you have Maintainer access to both DurpDeploy projects.
2. Ask your administrator to prepare a trusted DurpDeploy agent with Python 3.
3. Ask for agent labels that select only that machine.
4. Confirm that the machine can connect directly to GitLab and DurpDeploy through HTTPS.
5. Confirm that Python trusts both servers' certificates.
6. Restrict rotation project access to trusted operators.

A DurpDeploy agent is the machine that runs the script.
The **Host** setting runs it with that machine's operator permissions.
Your administrator can use the [agent setup guide](agents.md) to prepare it.

In GitLab, you need Maintainer or Owner access to the project.
Your role must be at least as high as the current token's role.
Your GitLab subscription and project policy must allow two active project tokens during the change.
Read [GitLab's project token requirements](https://docs.gitlab.com/user/project/settings/project_access_tokens/) if you cannot create another token.

## 1. Find the token and enroll its credential

1. In GitLab, open your project.
2. Select **Settings > Access tokens**.
3. Record the active token's name, creation date, expiry date, role, and scopes.
4. Confirm that no other active token has the same name.
5. In DurpDeploy, open the application project.
6. Open **Project menu > Credentials**.
7. Select **New credential or enroll secret**.

If `GITLAB_PROJECT_TOKEN` is an existing secret, find it under **Enroll an existing secret**.
Select **Enroll as credential** for its **All environments** entry.
Enrollment keeps its current value.
If it is already a credential, return to the credential list.

If you have not stored the token, complete **New credential variable**:

| Field | Enter or select |
| --- | --- |
| **Name** | `GITLAB_PROJECT_TOKEN` |
| **Credential value** | The current GitLab project token |
| **Environment** | **All environments** |
| **Expires at (UTC)** | The current token's expiry date at `00:00` UTC |

Select **Create credential**.

1. Select **Edit credential** beside `GITLAB_PROJECT_TOKEN`.
2. Record the two numbers in your browser's address bar.

For `/projects/42/credentials/variable/17/edit`, the application project ID is `42` and the credential ID is `17`.
These DurpDeploy IDs differ from GitLab's project and token IDs.

## 2. Create the two management tokens

The runbook needs permission to create a GitLab token and update the application credential.
It uses separate management tokens for these tasks.

1. In GitLab, create a [personal access token](https://docs.gitlab.com/user/profile/personal_access_tokens/) with the `api` scope.
2. Copy the personal access token before you leave its creation page.
3. In DurpDeploy, open `/settings/tokens` on your instance.
4. Enter a name such as `GitLab token rotation`.
5. Set **Scope** to **Selected projects**.
6. Select **Choose projects**.
7. Select only the application project.
8. Close the project picker.
9. Set **Token role** to `maintainer`.
10. Select **Create token**.
11. Copy the DurpDeploy token before you leave the page.

If you cannot open the token settings, ask your DurpDeploy administrator to create the scoped token.
The GitLab management token must be a personal access token.
A GitLab project access token cannot create replacement project tokens.

In the rotation project, open **Project menu > Credentials**.
For each row below, select **New credential or enroll secret** and complete **New credential variable**.
Use **All environments**.
Enter the token in **Credential value**.
Select **Create credential** after each entry.

| Name | Value |
| --- | --- |
| `GITLAB_ADMIN_TOKEN` | The GitLab personal access token that manages project tokens |
| `DD_API_TOKEN` | The DurpDeploy API token scoped to the application project |

Record each management token's expiry date when its provider supplies one.
Keep these tokens in credential fields, away from the script and ordinary variables.

## 3. Enter the runbook settings

1. In the rotation project, open **Project menu > Variables**.
2. In **New Variable**, enter a name and value from the table below.
3. Set **Environment** to **Unscoped**.
4. Leave **Secret** clear for these nonsecret settings.
5. Select **Add Variable**.
6. Repeat for each row.

| Name | Example value | Where to find your value |
| --- | --- | --- |
| `GITLAB_URL` | `https://gitlab.example.com` | Your GitLab address, without a project path |
| `GITLAB_PROJECT_ID` | `123` | The numeric project ID in GitLab's project overview |
| `GITLAB_TOKEN_NAME` | `durpdeploy-application` | The exact active token name from step 1 |
| `DD_URL` | `https://deploy.example.com` | Your DurpDeploy address, without a project path |
| `DD_PROJECT_ID` | `42` | The application project ID recorded in step 1 |
| `DD_CREDENTIAL_ID` | `17` | The credential ID recorded in step 1 |

Replace every example value with your own value.
Both addresses must start with `https://`.
Include only the server address and optional port, without a path, query, or embedded username and password.

For private certificates, ask your administrator for the certificate bundle path on the selected agent.
Add an optional variable named `SSL_CERT_FILE` with that path.
The bundle must trust both servers.

## 4. Create the runbook

1. In the rotation project, open **Project menu > Runbooks**.
2. Select **New runbook**.
3. Enter `Rotate GitLab project token` in **Name**.
4. Leave **Package from project release** set to **No package** in this dedicated project.
5. Select **Add step**.
6. Complete the step fields below.

| Field | Enter or select |
| --- | --- |
| **Step name** | `Replace GitLab token` |
| **Script** | The [copy-and-paste script](#copy-and-paste-script) below |
| **Interpreter** | **Python** |
| **Timeout (seconds)** | `180` |
| **Retries** | `0` |
| **Execution target** | **Agent** |
| **Agent labels (comma separated)** | The labels your administrator supplied |
| **Agent execution mode** | **Host** |
| **Limit variables (optional)** | The following comma-separated names |

```text
GITLAB_ADMIN_TOKEN, DD_API_TOKEN, GITLAB_URL, GITLAB_PROJECT_ID, GITLAB_TOKEN_NAME, DD_URL, DD_PROJECT_ID, DD_CREDENTIAL_ID
```

If you configured `SSL_CERT_FILE`, append it to **Limit variables (optional)**.

1. Select **Save** in the step dialog.
2. Select **Create new version** to save the runbook.
3. Leave **Schedule** empty.

Saving a runbook captures its configuration variables.
After changing those settings, use **Create new version** again before the next run.
Use **Credentials** to replace management tokens and update their matching saved runbook values.

## 5. Run the rotation

1. Confirm that nobody else is rotating this token or editing the application credential.
2. Open the saved runbook.
3. Under **Run now**, choose the environment where the selected agent is available.
4. Leave the version set to **Latest saved version**.
5. Select **Run book**.
6. Wait for the execution to succeed.
7. Record the previous and replacement GitLab token IDs from its log.

The log ends with `Replacement stored. Verify consumers before retiring the previous token.`
The script does not print either token value.
If the execution fails, follow [Recover from a failed run](#recover-from-a-failed-run) before trying again.

## 6. Check the replacement and retire the old token

1. Open the application project's **Project menu > Credentials**.
2. Find `GITLAB_PROJECT_TOKEN`.
3. Check **Last rotated (UTC)**, **Expires at (UTC)**, and **Rotate by (UTC)**.
4. Start a fresh application deployment or runbook that uses the token.
5. Confirm that its normal GitLab operation succeeds.
6. Wait for older executions to finish, or cancel them and confirm cleanup.
7. Update any tools outside this DurpDeploy project that use the old token.
8. In GitLab, open **Settings > Access tokens** for the project.
9. Identify the old token from the details you recorded before the run.
10. Select **Revoke** for that token.
11. Confirm **Revoke** in the dialog.
12. Confirm that only the replacement remains active with that name.

If the entries look identical, ask your GitLab administrator to identify the previous token ID from the runbook log.
Revoke the old token only after you identify it.

The script requests a 30-day lifetime and sets **Rotate by (UTC)** seven days before expiry.
Your GitLab instance must permit that lifetime.
Creating the replacement gives it the same scopes and role, with a new GitLab bot identity.
If your setup depends on the old bot identity, check those permissions before revocation.

DurpDeploy updates matching secrets in every release and saved runbook version in the application project.
Matches must have the same secret name and environment scope.
Older releases without that variable and other projects do not receive the replacement.
Already queued, approved, or running executions keep their captured token.
Fresh executions and re-runs use the replacement.

The project overview shows an expiry banner when stored dates need attention.
The banner does not check whether GitLab accepts the token.
Ask an administrator to check the rotation event in **Audit log** if you need an audit record.

## Recover from a failed run

Keep the old token active until a fresh deployment works with a known replacement.
A failed or timed-out run can leave a new GitLab token without saving it in DurpDeploy.
The runbook does not retry automatically.

| What you see | What to do |
| --- | --- |
| `Stopped during preflight` | Check IDs, names, permissions, certificate trust, and duplicate active token names before running again. |
| `Stopped during GitLab token creation` | Inspect GitLab's active tokens before retrying. GitLab might have created a token before the connection failed. |
| `Stopped during DurpDeploy credential publication` | Inspect the application credential and GitLab tokens. Ask an administrator to check for a conflicting credential edit. |
| The log reports a replacement token ID | Record that ID. Check its state before another run or any revocation. |
| The replacement fails a deployment | Keep the old token active. Check the new token's role, scopes, and bot permissions. |

If you cannot confirm which value DurpDeploy saved, use this recovery procedure:

1. In GitLab's **Settings > Access tokens**, select **Add new token**.
2. Enter the required name, expiry date, role, and scopes.
3. Select **Create project access token**.
4. Copy the new token before you leave the page.
5. In DurpDeploy, open the application project's **Credentials** page.
6. Select **Edit credential** for `GITLAB_PROJECT_TOKEN`.
7. Paste the new token into **Replacement credential**.
8. Update **Expires at (UTC)** and **Rotate by (UTC)**.
9. Select **Save**.
10. Verify a fresh deployment before revoking any old or unused tokens.
11. Restore one active token with the configured name before the next runbook execution.

Leaving **Replacement credential** blank only changes dates.
It does not replace the stored token.

## Copy-and-paste script

Copy the complete script into the runbook step's **Script** field.
Keep token values in **Credentials**.
The script uses the settings entered above and requires no additional Python packages.
It verifies HTTPS certificates and refuses redirects.

```python
import datetime as dt
import json
import os
import sys
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


client = urllib.request.build_opener(
    urllib.request.ProxyHandler({}), NoRedirect()
)


def origin(name):
    value = os.environ[name].rstrip("/")
    url = urllib.parse.urlsplit(value)
    if (url.scheme != "https" or not url.hostname or url.username
            or url.password or url.query or url.fragment or url.path):
        raise ValueError("An HTTPS origin is required")
    return value


def positive_id(name):
    value = int(os.environ[name])
    if value <= 0:
        raise ValueError("A positive ID is required")
    return value


def call(method, url, headers, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request(
        url, data=data, method=method,
        headers={**headers, "Content-Type": "application/json"},
    )
    with client.open(request, timeout=30) as response:
        return json.load(response), response.headers


phase = "preflight"
created_id = None
try:
    gitlab = origin("GITLAB_URL")
    deploy = origin("DD_URL")
    project_id = positive_id("GITLAB_PROJECT_ID")
    consumer_id = positive_id("DD_PROJECT_ID")
    credential_id = positive_id("DD_CREDENTIAL_ID")
    name = os.environ["GITLAB_TOKEN_NAME"]
    gitlab_auth = {"PRIVATE-TOKEN": os.environ["GITLAB_ADMIN_TOKEN"]}
    deploy_auth = {"Authorization": "Bearer " + os.environ["DD_API_TOKEN"]}
    credentials_url = f"{deploy}/api/v1/projects/{consumer_id}/credentials"
    rows, _ = call("GET", credentials_url, deploy_auth)
    matches = [row for row in rows if row["kind"] == "variable"
               and row["id"] == credential_id]
    if (len(matches) != 1 or matches[0]["name"] != "GITLAB_PROJECT_TOKEN"
            or matches[0]["environment_id"] is not None):
        raise ValueError("The enrolled consumer credential is required")
    credential = matches[0]
    tokens_url = f"{gitlab}/api/v4/projects/{project_id}/access_tokens"
    query = urllib.parse.urlencode(
        {"state": "active", "search": name, "per_page": 100}
    )
    rows, headers = call("GET", tokens_url + "?" + query, gitlab_auth)
    matches = [row for row in rows if row["name"] == name and row["active"]]
    if headers.get("X-Next-Page") or len(matches) != 1:
        raise ValueError("Resolve the active token list before rotation")
    previous = matches[0]
    previous_id = int(previous["id"])
    expires = dt.datetime.now(dt.timezone.utc).date() + dt.timedelta(days=30)
    phase = "GitLab token creation"
    replacement, _ = call("POST", tokens_url, gitlab_auth, {
        "name": name,
        "scopes": previous["scopes"],
        "access_level": previous["access_level"],
        "expires_at": expires.isoformat(),
    })
    created_id = int(replacement["id"])
    print(f"Previous GitLab token ID: {previous_id}", flush=True)
    print(f"Replacement GitLab token ID: {created_id}", flush=True)
    expiry = dt.datetime.strptime(replacement["expires_at"], "%Y-%m-%d")
    expiry = int(expiry.replace(tzinfo=dt.timezone.utc).timestamp())
    phase = "DurpDeploy credential publication"
    call("POST", credentials_url + f"/variable/{credential_id}/rotate",
         deploy_auth, {
             "value": replacement["token"],
             "revision": credential["revision"],
             "expires_at": expiry,
             "rotation_due_at": expiry - 7 * 24 * 60 * 60,
         })
    print("Replacement stored. Verify consumers before retiring the previous token.")
except Exception:
    print(f"Stopped during {phase}. Token values were not logged.", file=sys.stderr)
    if created_id is not None:
        print(f"Inspect GitLab token ID {created_id} before any retry.", file=sys.stderr)
    print("Check GitLab and the DurpDeploy audit log before retrying.", file=sys.stderr)
    sys.exit(1)
```
