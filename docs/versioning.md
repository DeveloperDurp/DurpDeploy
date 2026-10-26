# Software versions and build numbers

Run any version command without server configuration:

```bash
./durpdeploy version
./durpdeploy --version
./durpdeploy -v
```

These commands exit successfully without starting the server or opening a database.
The output includes the software version, build number, source commit, and reported source changes.

```text
durpdeploy v1.2.3 (build 742, commit 0123456789abcdef0123456789abcdef01234567)
```

## CI builds

CI checks out the full source history before it computes build metadata.
The build number is the result of `git rev-list --count HEAD`.
This count includes every commit reachable from the source commit, including merge commits.
Rebuilding the same source commit keeps the same build number.
Different branches can have the same count, so the source commit remains part of the identity.
History changes can change the count.

Untagged CI builds use a development version:

```text
v0.0.0-dev.742+g0123456789ab
```

CI injects this metadata into the executable through Go linker flags.
The Docker build receives the same values through build arguments.
The image labels include the software version, source commit, and build number.
The application reports the same values through its CLI, startup log, API, and web page.

OCI image tags cannot contain `+`.
CI replaces `+` with `_` only in the image tag and Helm `appVersion`.
For example, the development version above uses this image tag:

```text
harbor.durp.info/github/developerdurp/durpdeploy:v0.0.0-dev.742_g0123456789ab
```

CI builds and scans Linux AMD64 before publication.
Publication uses the current pipeline's scanned image and does not require matching merge request artifacts.
The publisher verifies the current build metadata and publishes the scanned image bytes.
It refuses to replace an existing version tag with different image bytes.

## Tagged releases

Create a tag with a `v` prefix and a valid semantic version, such as `v1.2.3`.
Push the tag through the normal release process.
Tagged CI builds embed that exact version and its source commit.
The build number remains the source commit count.
Prerelease identifiers and build metadata remain part of the embedded semantic version.
CI rejects invalid release tags.
CI does not choose or increment release numbers.

Protected default-branch pushes also create GitLab release pages after image and chart publication succeeds.
These pages use the existing development version and include changes since the previous release.
The automatic tag does not change the embedded software version or choose a stable release number.

## Local and container builds

Run `make build` to use the same metadata and linker mechanism as CI.
A clean checkout at an exact semantic release tag uses that version.
Other full-history checkouts use a counted development version.
Modified source adds `.dirty` to the development version and reports source changes.

Plain `go build` reports `dev`, an unknown build number, and available Go VCS metadata.
Source archives and shallow checkouts report unknown values when source history is unavailable.
They do not invent a commit count.

For a container build, compute metadata before the build context excludes `.git`:

```bash
source scripts/build_metadata.sh
podman build \
  --build-arg BUILD_VERSION="$BUILD_VERSION" \
  --build-arg BUILD_COMMIT="$BUILD_COMMIT" \
  --build-arg BUILD_NUMBER="$BUILD_NUMBER" \
  --build-arg BUILD_MODIFIED="$BUILD_MODIFIED" \
  -t "durpdeploy:$IMAGE_VERSION" .
podman run --rm "durpdeploy:$IMAGE_VERSION" version
```

Without these arguments, the container reports `dev` with unknown build information.

## API and web diagnostics

Call `GET /api/v1/version` with a bearer token.
All authenticated roles can read this endpoint.
The response contains `version`, `build_number`, `commit`, and `modified`.
Unknown build numbers and commits use the string `unknown`.

Open **Version** in the account menu to view `/settings/version`.
Viewers can read this page.
The fleet API and agent detail pages use the same server build identity.

## Agent versions

Read agent-reported software versions through the existing fleet API and Agents pages.
The fleet API preserves the nullable `agent_version` field.
The web page shows **Not reported** when an agent has not supplied a version.
Pairing alone does not supply this version through the current contract.
The first successful poll supplies the version, and later polls can replace it.

Agent-reported versions remain unverified.
Compare server and agent versions when you investigate an upgrade or execution problem.
Do not use software versions to select protocol capabilities.
Identifiers such as `agent/1`, `agent/2`, and `agent/3` describe the wire protocol separately.
Agent default version reporting is tracked in [agent issue #21](https://github.com/DeveloperDurp/durpdeploy-agent/issues/21).
