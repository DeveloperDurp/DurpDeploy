#!/usr/bin/env python3
"""Check deployment-access boundaries on a retained, loopback-only demo."""

import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import ssl
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request


PROJECTS_PATH = "/projects"
STEPS_PATH = "/steps"


PROBE = """set -eu
# The trusted fixture waits while the operator applies its runtime policy.
for attempt in $(seq 1 60); do
    test ! -e /tmp/access-policy-ready || break
    sleep 1
done
test -e /tmp/access-policy-ready
test "$(id -u)" = 65534
test ! -e /run/durpdeploy/runtime.sock
test ! -e /var/lib/durpdeploy/agent-identity
test ! -e /var/lib/durpdeploy-agent
grep -Eq '^CapEff:[[:space:]]+0+$' /proc/self/status
grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status
awk '$5 == "/" { if ($6 !~ /(^|,)ro(,|$)/) exit 1; found=1 } END { if (!found) exit 1 }' /proc/self/mountinfo
test "$(wc -l < /proc/net/route)" -eq 1
test "$(ls -A /sys/class/net)" = lo
for name in cpu.max memory.max memory.swap.max pids.max; do
    printf 'access-limit-%s=' "$name"
    if test -r "/sys/fs/cgroup/$name"; then
        cat "/sys/fs/cgroup/$name"
    else
        echo unavailable
    fi
done
# Writable data files must stay on memory-backed storage within memory.max.
awk '$6 ~ /(^|,)rw(,|$)/ { for (i=7; i<=NF; i++) if ($i == "-") { type=$(i+1); break }; if (type != "tmpfs" && type != "proc" && type != "devpts" && type != "mqueue" && type != "devtmpfs") exit 1 }' /proc/self/mountinfo
echo access-storage-boundary-ok
# Prove the explicit /tmp filesystem limit rejects growth with ENOSPC.
test "$(df -k /tmp | awk 'END {print $2}')" -eq 65536
if LC_ALL=C dd if=/dev/zero of=/tmp/access-disk-probe bs=1048576 count=65 2>/dev/shm/access-disk-error; then
    rm -f /tmp/access-disk-probe
    exit 1
fi
grep -q 'No space left on device' /dev/shm/access-disk-error
rm /tmp/access-disk-probe /dev/shm/access-disk-error
echo access-disk-limit-ok
echo access-boundary-ok
"""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        return None


def check_limits(limits):
    """Reject unlimited, malformed, and weaker-than-policy workload limits."""
    quota, period = map(int, limits["cpu.max"].split())
    memory = int(limits["memory.max"])
    swap = int(limits["memory.swap.max"])
    processes = int(limits["pids.max"])
    if not (0 < quota * 2 <= period and 0 < memory <= 128 * 1024 * 1024
            and swap == 0 and 0 < processes <= 128):
        raise ValueError("Workload exceeds the demo resource policy")


def check_trust(hosts):
    """A runtime host must belong to exactly one script-author trust domain."""
    if not hosts or any(len(set(levels)) != 1 or not all(levels)
                        for levels in hosts.values()):
        raise ValueError("Do not share an execution host between trust levels")


class DemoAccessTest:
    def __init__(self, directory):
        self.directory = directory.resolve(strict=True)
        self.base = (self.directory / "url").read_text().strip()
        url = urllib.parse.urlsplit(self.base)
        if url.scheme != "https" or url.hostname != "127.0.0.1":
            raise ValueError("Use a DEMO_LOCAL_ONLY=1 demo")
        self.name = (self.directory / "container").read_text().strip()
        if not re.fullmatch(r"durpdeploy-demo-[A-Za-z0-9]+", self.name):
            raise ValueError("Invalid demo container metadata")
        self.prefix = "access-" + secrets.token_hex(5)
        self.evidence = self.directory / self.prefix
        self.evidence.mkdir(mode=0o700)
        self.origin = self.base
        self.engine = (self.directory / "runtime").read_text().strip()
        self.gateway = self.name + "-" + self.prefix
        self.gateway_id = None
        self.gateway_started = False
        self.complete = False
        self.records = []
        self.tokens = []
        self.deployments = []
        self.context = ssl.create_default_context(
            cafile=str(self.directory / "tls/cert.pem")
        )
        self.jar = http.cookiejar.CookieJar()
        self.web = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=self.context),
            urllib.request.HTTPCookieProcessor(self.jar), NoRedirect(),
        )
        self.api_client = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=self.context), NoRedirect(),
        )

    def runtime(self, *args):
        return subprocess.run([self.engine, *args], check=True, capture_output=True,
                              text=True, timeout=45).stdout.strip()

    def start_gateway(self):
        if Path(self.engine).name != "podman" or self.runtime(
                "info", "--format", "{{.Host.Security.Rootless}}") != "true":
            raise ValueError("This fixture requires local rootless Podman")
        proxy = json.loads(self.runtime("inspect", self.name + "-https"))[0]
        bindings = proxy["HostConfig"]["PortBindings"]
        if any(port["HostIp"] != "127.0.0.1"
               for ports in bindings.values() for port in (ports or [])):
            raise ValueError("The original demo proxy must remain loopback-only")
        address = next(json.loads(line)["addr"] for line in
                       (self.directory / "server.log").read_text().splitlines()
                       if '"msg":"server starting"' in line)
        host, port = address.rsplit(":", 1)
        if host != "127.0.0.1" or not port.isdigit():
            raise ValueError("The demo backend must remain loopback-only")
        tls = self.evidence / "gateway"
        tls.mkdir(mode=0o700)
        for name in ("cert.pem", "key.pem"):
            shutil.copyfile(self.directory / "tls" / name, tls / name)
        for name in ("ca", "foreign"):
            self.openssl(tls, "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                         "-days", "2", "-subj", "/CN=" + name,
                         "-keyout", name + ".key", "-out", name + ".pem")
        self.openssl(tls, "req", "-newkey", "rsa:2048", "-nodes",
                     "-subj", "/CN=demo-operator", "-keyout", "client.key",
                     "-out", "client.csr")
        (tls / "client.ext").write_text("extendedKeyUsage=clientAuth\n")
        self.openssl(tls, "x509", "-req", "-in", "client.csr", "-CA", "ca.pem",
                     "-CAkey", "ca.key", "-CAcreateserial", "-days", "2",
                     "-extfile", "client.ext", "-out", "client.pem")
        (tls / "Caddyfile").write_text(
            ":8443 {\n tls /demo/cert.pem /demo/key.pem {\n"
            "  client_auth {\n   mode require_and_verify\n"
            "   trust_pool file /demo/ca.pem\n  }\n }\n"
            f" reverse_proxy host.containers.internal:{port}\n}}\n")
        exported = tls / "server"
        exported.mkdir(mode=0o700)
        for name in ("cert.pem", "key.pem", "ca.pem", "Caddyfile"):
            shutil.copyfile(tls / name, exported / name)
        network = ("slirp4netns:allow_host_loopback=true" if shutil.which("slirp4netns")
                   else "pasta:--map-host-loopback,169.254.1.2")
        self.gateway_started = True
        self.gateway_id = self.runtime(
            "run", "-d", "--rm", "--name", self.gateway,
            "--label", "io.durpdeploy.conformance=" + self.prefix,
            "--network", network, "--read-only", "--cap-drop=ALL",
            "--cap-add=NET_BIND_SERVICE", "--security-opt=no-new-privileges:true",
            "--tmpfs=/data:size=16m", "--tmpfs=/config:size=16m",
            "--tmpfs=/tmp:size=16m", "-p", "127.0.0.1::8443",
            "-v", str(exported) + ":/demo:ro,Z",
            "harbor.durp.info/dockerhub/library/caddy:2-alpine",
            "caddy", "run", "--config", "/demo/Caddyfile", "--adapter", "caddyfile")
        published = self.runtime("port", self.gateway_id, "8443/tcp")
        if not re.fullmatch(r"127\.0\.0\.1:[0-9]+", published):
            raise ValueError("Gateway is not bound exclusively to loopback")
        # Client authentication requires SNI. TLS clients omit SNI for IP literals.
        self.base = "https://localhost:" + published.rsplit(":", 1)[1]
        self.context.load_cert_chain(tls / "client.pem", tls / "client.key")
        for attempt in range(50):
            try:
                self.request("GET", "/healthz")
                break
            except (urllib.error.URLError, ssl.SSLError):
                if attempt == 49:
                    raise
                time.sleep(0.1)
        for name in (None, "foreign"):
            context = ssl.create_default_context(cafile=str(tls / "cert.pem"))
            if name:
                context.load_cert_chain(tls / (name + ".pem"), tls / (name + ".key"))
            try:
                with urllib.request.urlopen(self.base + "/healthz", context=context,
                                            timeout=5) as response:
                    response.read()
            except (urllib.error.URLError, ssl.SSLError) as error:
                reason = error if isinstance(error, ssl.SSLError) else error.reason
                if not isinstance(reason, ssl.SSLError):
                    raise
                self.records.append({"origin": self.base, "path": "/healthz",
                                     "client": name or "absent", "tls_rejected": True})
            else:
                raise AssertionError("Gateway admitted an unauthorized TLS client")

    @staticmethod
    def openssl(directory, *args):
        subprocess.run(["openssl", *args], cwd=directory, check=True,
                       capture_output=True, timeout=30)

    def apply_limits(self, deployment):
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            ids = self.runtime("ps", "-q", "--filter",
                               "label=io.durpdeploy.namespace=podman:" + self.name,
                               "--filter", f"name=^durpdeploy-{deployment}-1-").splitlines()
            if ids:
                break
            time.sleep(0.1)
        if len(ids) != 1:
            raise AssertionError("Expected exactly one owned probe workload")
        worker = json.loads(self.runtime("inspect", ids[0]))[0]
        labels = worker["Config"]["Labels"]
        if (labels.get("io.durpdeploy.namespace") != "podman:" + self.name
                or not re.fullmatch(f"durpdeploy-{deployment}-1-[0-9a-f]{{24}}",
                                    labels.get("io.durpdeploy.attempt", ""))):
            raise ValueError("Refusing to update an unrelated workload")
        self.runtime("update", "--cpus=0.5", "--memory=128m", "--memory-swap=128m", ids[0])
        policy = json.loads(self.runtime("inspect", ids[0]))[0]["HostConfig"]
        (self.evidence / "runtime-policy.json").write_text(json.dumps({
            "container": ids[0], "labels": labels,
            "policy": {key: policy[key] for key in (
                "NanoCpus", "CpuQuota", "CpuPeriod", "Memory", "MemorySwap",
                "PidsLimit", "ReadonlyRootfs", "NetworkMode", "Tmpfs")},
        }, indent=2) + "\n")
        self.runtime("exec", "--user=65534:65534", ids[0],
                     "touch", "/tmp/access-policy-ready")

    def request(self, method, path, token="", payload=None, want=200, web=False):
        headers = {}
        if token:
            headers["Authorization"] = "Bearer " + token
        if web:
            data = urllib.parse.urlencode(payload).encode() if payload else None
            headers["Content-Type"] = "application/x-www-form-urlencoded"
            client = self.web
        else:
            data = json.dumps(payload).encode() if payload is not None else None
            headers["Content-Type"] = "application/json"
            client = self.api_client
        request = urllib.request.Request(
            self.base + path, data=data, headers=headers, method=method
        )
        try:
            response = client.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read().decode()
            # Authentication material and submitted passwords are not evidence.
            safe_body = re.sub(r"ddp_pat_[0-9a-f]{64}", "[REDACTED]", body)
            if path.startswith("/api/v1/admin/users") and method == "POST":
                safe_body = "[user creation response omitted]"
            self.records.append({
                "origin": self.base, "method": method, "path": path, "status": response.status,
                "content_type": response.headers.get("Content-Type"),
                "body": safe_body if not web else "[web body omitted]",
            })
            if response.status != want:
                raise AssertionError(f"{method} {path}: {response.status}, want {want}")
            return body

    def api(self, method, path, token="", payload=None, want=200):
        return json.loads(self.request(
            method, "/api/v1" + path, token, payload, want
        ))

    def token(self, email, project=None):
        env = os.environ.copy()
        env["DURPDEPLOY_DB"] = str(self.directory / "durpdeploy.db")
        env["DURPDEPLOY_SECRET_KEY"] = (self.directory / "secret-key").read_text().strip()
        command = [str(self.directory / "bin/durpdeploy"), "tokens", "create",
                   "--user", email, "--name", self.prefix]
        if project is not None:
            command += ["--scope", "scoped", "--role", "deployer",
                        "--project-id", str(project)]
        token = subprocess.run(command, env=env, check=True, capture_output=True,
                               text=True, timeout=30).stdout.strip()
        if not re.fullmatch(r"ddp_pat_[0-9a-f]{64}", token):
            raise ValueError("CLI did not return a token")
        self.tokens.append((token, env))
        return token

    def inventory(self, admin, project, environment, actors):
        users = []
        while True:
            page = self.api("GET", f"/admin/users?limit=100&offset={len(users)}", admin)
            users.extend(page["items"])
            if len(users) >= page["total"]:
                break
            if not page["items"]:
                raise AssertionError("User inventory pagination did not advance")
        members = self.api("GET", f"/projects/{project}/members", admin)["items"]
        roles = {member["user_id"]: member["role"] for member in members}
        expected = {actor["id"]: role for role, actor in actors.items()}
        owner = next(user for user in users if user["email"] == "admin@durp.info")
        if owner["role"] != "admin":
            raise AssertionError("Unexpected demo operator role")
        expected[owner["id"]] = "admin"
        if roles != expected:
            raise AssertionError("Unexpected fixture project membership")
        host = self.runtime("info", "--format", "{{.Host.Hostname}}")
        hosts = {host: ["trusted-demo-operators"]}
        check_trust(hosts)
        inventory = {
            "scope": "Retained demo project only; one trusted script-author domain",
            "project": project, "environment": environment,
            "authors": [user for user in users if user["role"] == "admin"
                        or user["id"] == actors["maintainer"]["id"]],
            "execution_only": actors["deployer"], "project_members": members,
            "execution_hosts": hosts,
            "resources": {"runtime": "local rootless Podman", "mode": "server container",
                          "image": "harbor.durp.info/dockerhub/library/bash:5.2",
                          "network": "none", "host_mounts": [], "secrets": [],
                          "writable_job_storage": "/tmp: 64 MiB tmpfs; all writable data "
                                                  "files use tmpfs within the 128 MiB "
                                                  "cgroup memory limit, with no swap",
                          "cpu": "0.5 CPU", "memory_bytes": 134217728,
                          "processes": 128, "step_timeout_seconds": 120},
            "trust_rule": "All scripts on this demo host require operator trust. "
                          "Use separate hosts for any additional trust domain. "
                          "Containers share this host's kernel.",
        }
        # Check the rejection rule as well as the configured single-domain case.
        try:
            check_trust({host: ["trusted-demo-operators", "untrusted-authors"]})
        except ValueError:
            pass
        else:
            raise AssertionError("Mixed trust domains passed the inventory check")
        (self.evidence / "inventory.json").write_text(json.dumps(inventory, indent=2) + "\n")

    def run(self):
        self.start_gateway()
        admin = self.token("admin@durp.info")
        self.request("GET", PROJECTS_PATH, want=303)
        self.api("GET", PROJECTS_PATH, want=401)
        project = self.api("POST", PROJECTS_PATH, admin, {
            "name": self.prefix, "description": "Retained #110 demo acceptance test."
        }, 201)["id"]
        environment = self.api("POST", "/environments", admin,
                               {"name": self.prefix}, 201)["id"]
        root = f"/projects/{project}"
        password = "demo-" + secrets.token_hex(16)
        actors = {}
        for role in ("maintainer", "deployer"):
            email = f"{self.prefix}-{role}@test.local"
            user = self.api("POST", "/admin/users", admin, {
                "email": email, "name": self.prefix + " " + role,
                "password": password, "role": role,
            }, 201)["id"]
            self.api("POST", root + "/members", admin,
                     {"user_id": user, "role": role}, 201)
            actors[role] = {"email": email, "id": user}
        (self.evidence / "login.json").write_text(json.dumps({
            "accounts": actors, "password": password
        }, indent=2) + "\n")
        author = self.token(actors["maintainer"]["email"])
        executor = self.token(actors["deployer"]["email"], project)
        self.inventory(admin, project, environment, actors)
        step = {"name": "Boundary probe", "script_body": PROBE,
                "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
                "timeout_seconds": 120}
        self.api("POST", root + STEPS_PATH, author, step, 201)
        release = self.api("POST", root + "/releases", author,
                           {"version": "approved-v1"}, 201)["id"]
        for path, body in ((STEPS_PATH, step), ("/variables", {"name": "DENIED", "value": "x"}),
                           ("/releases", {"version": "denied"})):
            self.api("POST", root + path, executor, body, 403)
        self.api("POST", root + "/members", executor,
                 {"user_id": actors["deployer"]["id"], "role": "admin"}, 403)
        self.request("POST", "/login", payload={
            "email": actors["deployer"]["email"], "password": password
        }, want=303, web=True)
        page = self.request("GET", "/", web=True)
        csrf = re.search(r'<meta name="csrf-token" content="([^"]+)"', page)[1]
        self.request("POST", root + STEPS_PATH, payload={
            **step, "csrf_token": csrf
        }, want=403, web=True)
        deployment = self.api("POST", root + "/deployments", executor, {
            "release_id": release, "environment_id": environment
        }, 201)["id"]
        self.deployments.append(deployment)
        self.apply_limits(deployment)
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            status = self.api("GET", f"/deployments/{deployment}/status", executor)["status"]
            if status in ("succeeded", "failed", "cancelled", "cleanup_unconfirmed"):
                break
            time.sleep(0.25)
        if status != "succeeded":
            raise AssertionError(f"Probe deployment {deployment}: {status}")
        logs = self.request("GET", f"/api/v1/deployments/{deployment}/logs.txt", executor)
        if any(marker not in logs for marker in (
                "access-boundary-ok", "access-disk-limit-ok", "access-storage-boundary-ok")):
            raise AssertionError("Workload boundary probe did not complete")
        limits = dict(re.findall(r"access-limit-([^=\n]+)=([^\r\n]+)", logs))
        check_limits(limits)
        (self.evidence / "workload.log").write_text(logs)
        self.api("PUT", root + f'/members/{actors["deployer"]["id"]}', admin,
                 {"role": "viewer"})
        self.api("POST", root + "/deployments", executor, {
            "release_id": release, "environment_id": environment
        }, 403)
        # Restore the disposable identity for retained manual checks.
        self.api("PUT", root + f'/members/{actors["deployer"]["id"]}', admin,
                 {"role": "deployer"})
        return {
            "scope": "configured single-trust-domain demo only", "project": project,
            "deployment": deployment, "actors": actors,
            "permitted_resource": "demo server container runtime",
            "permissions": "PASS", "workload_boundary": "PASS",
            "observed_cgroup_limits": limits,
            "controls": {"trusted_author_inventory": "PASS", "deployer_role": "PASS",
                         "access_gateway": "PASS", "host_trust_and_runtime_limits": "PASS"},
            "gateway": self.base, "gateway_container": self.gateway,
            "project_url": self.origin + root,
            "deployment_url": self.origin + f"/deployments/{deployment}",
            "limits_scope": "The held trusted fixture receives operator runtime limits "
                            "before its probe. This does not add application defaults. "
                            "The probe has no staging volume or writable host mounts.",
        }

    def finish_deployment(self, deployment, admin):
        path = f"/deployments/{deployment}"
        status = self.api("GET", path + "/status", admin)["status"]
        if status not in ("succeeded", "failed", "cancelled"):
            self.api("POST", path + "/cancel", admin)
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline:
                status = self.api("GET", path + "/status", admin)["status"]
                if status in ("succeeded", "failed", "cancelled"):
                    break
                time.sleep(0.25)
        return status in ("succeeded", "failed", "cancelled")

    def finish(self):
        errors = []
        # Cleanup must still reach the private demo if the new gateway fails.
        self.base = self.origin
        try:
            if self.tokens:
                admin = self.tokens[0][0]
                for deployment in self.deployments:
                    if not self.finish_deployment(deployment, admin):
                        errors.append(f"Deployment {deployment} needs cleanup inspection")
        finally:
            for token, env in self.tokens:
                try:
                    result = subprocess.run([
                        str(self.directory / "bin/durpdeploy"), "tokens", "revoke", token[:12]
                    ], env=env, capture_output=True, timeout=30)
                except (OSError, subprocess.SubprocessError):
                    errors.append("Could not revoke a test token")
                    continue
                if result.returncode:
                    errors.append("Could not revoke a test token")
            (self.evidence / "http.json").write_text(json.dumps(self.records, indent=2) + "\n")
            if self.gateway_started and not self.complete:
                try:
                    # A timed-out run can leave a container without returning its ID.
                    owned = self.runtime("ps", "-aq", "--filter", "name=^" + self.gateway + "$",
                                         "--filter", "label=io.durpdeploy.conformance=" + self.prefix)
                    for container in owned.splitlines():
                        self.runtime("rm", "--force", container)
                except (OSError, subprocess.SubprocessError):
                    errors.append("Could not remove the failed test gateway")
        if errors:
            raise RuntimeError("; ".join(errors))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("demo", type=Path)
    args = parser.parse_args()
    test = DemoAccessTest(args.demo)
    result = {"issue110_complete": False}
    try:
        result = test.run()
        test.complete = True
    finally:
        try:
            test.finish()
        except Exception:
            result["issue110_complete"] = False
            raise
        finally:
            (test.evidence / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    result["issue110_complete"] = all(value == "PASS" for value in result["controls"].values())
    (test.evidence / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))
    print(f"Evidence: {test.evidence}")
    print(f"Stop gateway: {test.engine} stop {test.gateway}")
    return 0 if result["issue110_complete"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
