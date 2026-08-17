# Authentik OIDC setup for DurpDeploy

This guide configures Authentik as the OpenID Connect provider for DurpDeploy
browser sign-in. OIDC is optional. The local password form remains available,
so configure it before testing SSO and keep an administrator recovery path.

Authentik changes its menu names and layout between releases. The labels below
describe the current concepts, not a promise that every screen has the same
name. Use the Authentik documentation for your installed version when a label
differs.

## Values used by this guide

Choose three distinct Authentik group names. The examples below are placeholders
only. The strings must match the groups emitted in the `groups` claim exactly.

| DurpDeploy role | Authentik group string |
| --- | --- |
| Admin | `<durpdeploy-admin-group>` |
| Deployer | `<durpdeploy-deployer-group>` |
| Viewer | `<durpdeploy-viewer-group>` |

If a user belongs to more than one configured group, DurpDeploy applies this
precedence: `admin`, then `deployer`, then `viewer`. A partial or similar group
name does not match. Group names are role bindings, so protect membership in
these groups as carefully as local administrator access.

## 1. Create the Authentik application and provider

In the Authentik administrator interface:

Before configuring the client details, select a provider `Signing Key`. DurpDeploy
verifies ID tokens using the provider's published JWKS, so this integration needs
asymmetric token signatures that can be checked through that JWKS. Selecting the
default `authentik Self-signed Certificate` is sufficient for local or setup use.
For production, an operator-managed certificate and key may be more appropriate.
This is a DurpDeploy integration prerequisite, not a requirement for every
Authentik OIDC client.

1. Create an application for DurpDeploy. Give it a display name such as
   `DurpDeploy` and attach a new OAuth2/OIDC provider. In newer Authentik
   releases, the application and provider can be created from one flow. If
   your release separates them, create the provider first and attach it to the
   application.
2. Select an authorization-code style OIDC flow. Do not use an implicit flow
   for this server integration.
3. Set the provider's client type to confidential. Copy the generated client
   ID and client secret into the deployment secret store. Never put the secret
   in source control, tickets, screenshots, or this guide.
4. Set the provider's issuer to the exact issuer URL shown by Authentik for
   this provider, including any provider path and trailing slash. For example,
   Authentik may show `https://authentik.example/application/o/durpdeploy/`.
   Do not omit the path, add or remove a trailing slash, or manually alter the
   value; reject only a value with a query or fragment. Use the same value for
   `DURPDEPLOY_OIDC_ISSUER`.
5. Add one strict authorization redirect URI:

   ```text
   ${DURPDEPLOY_URL}/login/oidc/callback
   ```

   Replace the placeholder with the same absolute HTTPS origin used by
   `DURPDEPLOY_URL`. Do not add a wildcard, a trailing path variant, or an
   HTTP URI. DurpDeploy does not configure an upstream logout redirect.
6. Allow these exact scopes, in any UI order: `openid profile email`.

The issuer and the DurpDeploy public URL must both use HTTPS. The issuer may
include Authentik's required provider path and trailing slash; copy it exactly.
The DurpDeploy canonical public URL, unlike the issuer, must have no path, query,
or fragment. The callback is derived from that URL, so a proxy URL mismatch
causes a redirect or state failure.

## 2. Configure scopes and claims

Authentik scope mappings determine which claims are put into the ID token and
UserInfo response. DurpDeploy uses the verified ID token. It does not call
UserInfo. Make sure the ID token itself contains the claims below.

The built-in `profile` mapping commonly includes `name` and `groups`, but the
available mappings and their values vary by Authentik version. The built-in
email mapping may not assert a verified email for every installation. Create a
custom scope mapping for the provider, or adjust a provider-specific mapping,
so the ID token contains:

* `email`, the user's email address.
* `email_verified`, the boolean value `true` for accounts your policy permits
  to sign in.
* `DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED` is optional and defaults to `true` in DurpDeploy. When unset, or set to `true`, the ID token must contain the literal JSON boolean `email_verified: true`. Explicit lowercase `false` accepts a present literal JSON boolean `email_verified: true` or `email_verified: false` after normal ID token signature, issuer, audience, and nonce verification. Missing, null, string, and numeric claims remain rejected. This weakens identity assurance and is appropriate only where Authentik independently establishes address ownership.
* `name`, when you want Authentik to supply the display name.
* `groups`, an array of strings containing the exact configured group names.

Attach that mapping to the provider's selected scopes, normally under the
`profile` scope or a dedicated scope that is requested by the client. If you
use a dedicated scope, keep the requested DurpDeploy scopes unchanged and add
the mapping to the provider so it is included for the `profile` request.

An Authentik property mapping expression can return a dictionary containing
the required claim names. The group value should be built from the user's
actual group names, not from display labels or group objects. Set the verified
flag only from an enrollment or directory rule that your organization trusts.
Do not set `email_verified` to true merely because the user has an Authentik
account.

After saving the mapping, inspect the provider's token preview or a test
login using Authentik's version-appropriate tools. Verify the claims without
copying tokens or full claim payloads into logs or tickets. When unset, or set
to `true`, DurpDeploy requires the callback ID token to contain the literal
boolean `email_verified: true`. Explicit lowercase
`DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false` accepts a present literal JSON
boolean `email_verified: true` or `email_verified: false` after normal ID token
signature, issuer, audience, and nonce verification. Missing, null, string, and
numeric claims remain rejected. This weakens
identity assurance and is appropriate only where Authentik independently
establishes address ownership.

## 3. Create and assign the role groups

Create the three groups if they do not exist, using the exact strings selected
above. Add test users to one group at a time first. Then assign the groups to
the Authentik application or provider using the access policy or group policy
mechanism available in your release.

For an operator who belongs to both admin and deployer, DurpDeploy still stores
`admin`. For a user who belongs to deployer and viewer, it stores `deployer`.
Removing all three groups does not immediately remove access from an existing
local session. The changed mapping is applied on that user's next successful
OIDC login.

## 4. Deploy DurpDeploy with OIDC enabled

OIDC is disabled unless at least one OIDC-specific variable is set. Once it is
enabled, all required values below must be present. The group values must be
distinct. The display name and group claim have defaults, but setting them
explicitly makes the deployment easier to audit.

```ini
DURPDEPLOY_URL=https://<durpdeploy-public-host>
DURPDEPLOY_OIDC_ISSUER=https://<authentik-issuer-host>
DURPDEPLOY_OIDC_CLIENT_ID=<authentik-client-id>
DURPDEPLOY_OIDC_CLIENT_SECRET=<authentik-client-secret>
DURPDEPLOY_OIDC_ADMIN_GROUP=<durpdeploy-admin-group>
DURPDEPLOY_OIDC_DEPLOYER_GROUP=<durpdeploy-deployer-group>
DURPDEPLOY_OIDC_VIEWER_GROUP=<durpdeploy-viewer-group>
DURPDEPLOY_OIDC_DISPLAY_NAME=SSO
DURPDEPLOY_OIDC_GROUP_CLAIM=groups
DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=<true|false>
```

The required values are `DURPDEPLOY_URL`, the issuer, client ID, client
secret, and all three role group variables. `DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED`
defaults to `true`; set it to explicit lowercase `false` only where Authentik
independently establishes address ownership. This weakens identity assurance
and accepts a present literal JSON boolean `email_verified: true` or
`email_verified: false` claim; missing, null, string, and numeric claims remain
rejected. `DURPDEPLOY_OIDC_DISPLAY_NAME`
defaults to `SSO`.
`DURPDEPLOY_OIDC_GROUP_CLAIM` defaults to `groups`.

For systemd, put the values in a root-owned environment file readable only by
the service, or use the existing deployment mechanism. For Compose, keep the
secret in the environment or secret store and reference it from the service.
Do not paste the client secret into a shell history, unit committed to git, or
public issue. Restart DurpDeploy after changing the provider settings or
environment values. Discovery is lazy, so a startup that succeeds does not
prove the issuer is reachable.

## 5. Verify the complete path

Use a test account before changing production group membership.

- [ ] `DURPDEPLOY_URL` is the one public HTTPS origin users open.
- [ ] Authentik has exactly the callback
      `${DURPDEPLOY_URL}/login/oidc/callback`, with no wildcard.
- [ ] The issuer is copied from the Authentik provider and is HTTPS.
- [ ] The client is confidential and its secret is stored outside source
      control.
- [ ] The requested scopes are exactly `openid profile email`.
- [ ] The ID token contains `email`, `email_verified`, and `groups`.
- [ ] The three configured group strings are exact and distinct.
- [ ] A user in the admin group becomes `admin`, a deployer user becomes
      `deployer`, and a viewer user becomes `viewer`.
- [ ] A user in multiple configured groups receives the highest-precedence
      role, admin over deployer over viewer.
- [ ] An email matching one local account links to that account. When unset or `true`, the ID token has literal JSON boolean `email_verified: true`; with explicit lowercase `false`, it has a present literal JSON boolean `email_verified: true` or `email_verified: false`. Missing, null, string, and numeric claims are rejected.
- [ ] A verified email with no local match creates a JIT user with an empty
      password.
- [ ] Password login still works when the provider is unavailable.
- [ ] Local logout ends the DurpDeploy session, while a later SSO start may
      still use the Authentik session.

After the first test, inspect the user's role and session state in DurpDeploy.
Do not record the client secret, authorization code, access token, ID token, or
raw claims as verification evidence.

## Troubleshooting

### Redirect URI mismatch

Compare the configured URI character for character with
`${DURPDEPLOY_URL}/login/oidc/callback`. Check the external proxy origin,
scheme, port, and trailing slash. DurpDeploy requires the fixed callback and
does not accept wildcard redirect URIs.

### OIDC is not shown on the login page

Check that at least one OIDC-specific variable is set and that all required
variables are nonempty. `DURPDEPLOY_URL` alone does not enable OIDC. Check the
service environment, not only the shell where the file was edited, then
restart the service.

### `email_verified` or groups are missing

The scope mapping may be attached to the wrong provider, selected scope, or
token type. Confirm that the mapping is selected by this provider and that the
claims are in the ID token. Use the exact claim name configured in
`DURPDEPLOY_OIDC_GROUP_CLAIM`; the default is `groups`. Confirm that group
values are strings and that the configured names match exactly.

### The user gets the wrong role

Review all three group memberships and the configured values. DurpDeploy uses
admin, then deployer, then viewer precedence. A role is synchronized only on a
successful OIDC login. A role change removes the user's DurpDeploy browser
sessions, so the user must sign in again.

### Removing a group did not block the user immediately

There is no SCIM integration or provider back-channel deprovisioning. Group
removal takes effect on the next SSO login for that user. Existing sessions do
not continuously recheck Authentik membership. Revoke local sessions or remove
the local account through the normal administrative recovery process when an
urgent response requires it.

### Authentik is unavailable

The OIDC attempt fails generically, but password login, existing DurpDeploy
sessions, health checks, and bearer API authentication remain available. This
is deliberate provider isolation, not a signal to disable TLS verification.

## Security limits and coexistence

OIDC is an optional login factor, not a replacement for local authentication.
The first email match links exactly one local account. A new email creates a JIT user with an empty password. Whether the email must be verified depends on `DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED` (default `true`). That user has no self-service
password reset; an administrator must use the existing local recovery process.

Each successful SSO login synchronizes the local name, email, and role. Password
login remains available beside SSO and uses the most recently stored local role.
OIDC reauthentication is performed by Authentik and then bound to the current
DurpDeploy session and stored OIDC identity. DurpDeploy does not persist
provider tokens, authorization codes, or raw claims. OIDC does not use UserInfo,
does not authenticate API tokens, and does not assert local MFA.

Logout is local only. It clears the DurpDeploy session, not the Authentik
session, and there is no upstream logout. Closing the browser or using the
provider's own logout is separate from DurpDeploy logout.

These limits are part of the current behavior. Review [`docs/security.md`](security.md),
[`docs/roles.md`](roles.md), and [`docs/attack-drill.md`](attack-drill.md) before
using group membership as an emergency access control.
