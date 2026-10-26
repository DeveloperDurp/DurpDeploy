#!/usr/bin/env python3
"""Delegate resource controllers inside the current GitLab build container."""

import os
import pwd
import re
import subprocess
from pathlib import Path

PROCESSES = "cgroup.procs"
SUBTREE_CONTROL = "cgroup.subtree_control"
CGROUP_ROOT = Path("/sys/fs/cgroup")


def move_build_processes(directory, bootstrap):
    # Empty only this build container's leaf before enabling domain controllers.
    for _ in range(100):
        processes = (directory / PROCESSES).read_text().split()
        if not processes:
            break
        for process in processes:
            try:
                (bootstrap / PROCESSES).write_text(process + "\n")
            except ProcessLookupError:
                pass
    else:
        raise RuntimeError("CI build cgroup did not become empty")


def prepare(directory, uid, gid):
    required = {"cpu", "memory", "pids"}
    available = set((directory / "cgroup.controllers").read_text().split())
    if not required <= available:
        raise RuntimeError("CI build cgroup requires cpu, memory, and pids controllers")
    if (directory / "cgroup.type").read_text().strip() != "domain":
        raise RuntimeError("CI build cgroup must be a domain")
    bootstrap = directory / "durpdeploy-bootstrap"
    delegated = directory / "durpdeploy-rootless"
    if bootstrap.exists() or delegated.exists():
        raise RuntimeError("CI cgroup setup requires a fresh build container")
    bootstrap.mkdir(mode=0o700)
    move_build_processes(directory, bootstrap)
    controllers = "+cpu +memory +pids"
    (directory / SUBTREE_CONTROL).write_text(controllers)
    delegated.mkdir(mode=0o700)
    (delegated / SUBTREE_CONTROL).write_text(controllers)
    process_leaf = delegated / "processes"
    process_leaf.mkdir(mode=0o700)
    for group in [delegated, process_leaf]:
        os.chown(group, uid, gid)
        for name in [PROCESSES, "cgroup.threads", SUBTREE_CONTROL]:
            os.chown(group / name, uid, gid)
    return delegated


def main():
    if os.geteuid() != 0 or os.environ.get("GITLAB_CI") != "true" or os.environ.get("CI_RUNTIME") != "podman":
        raise RuntimeError("CI cgroup setup requires the root GitLab Podman job")
    mount = subprocess.check_output(
        ["findmnt", "-n", "-o", "FSTYPE,FSROOT", "/sys/fs/cgroup"], text=True
    ).split()
    if mount != ["cgroup2", "/"]:
        raise RuntimeError("CI cgroup setup requires the unified hierarchy mount")
    memberships = Path("/proc/self/cgroup").read_text().splitlines()
    relative = next((line[3:] for line in memberships if line.startswith("0::")), "")
    # Refuse host roots, pod parents, other hierarchies, and path traversal.
    if not re.fullmatch(r"/kubepods/(?:\w[\w.:-]*/)+[a-f0-9]{64}", relative, re.ASCII):
        raise RuntimeError("CI cgroup setup requires a Kubernetes build-container leaf")
    directory = CGROUP_ROOT / relative.lstrip("/")
    if directory.resolve() != directory or directory.stat().st_uid != 0:
        raise RuntimeError("CI build cgroup must be a root-owned canonical path")
    user = pwd.getpwnam("pwuser")
    if user.pw_uid == 0:
        raise RuntimeError("CI rootless user must not be root")
    delegated = prepare(directory, user.pw_uid, user.pw_gid)
    print("/" + str(delegated.relative_to(CGROUP_ROOT)))


if __name__ == "__main__":
    main()
