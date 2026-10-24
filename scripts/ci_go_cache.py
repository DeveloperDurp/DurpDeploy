#!/usr/bin/env python3
"""Transfer Go build objects added to the immutable CI helper cache."""

import json
import os
from pathlib import Path
import pwd
import re
import shutil
import stat
import sys
import time

OBJECT = re.compile(r"[0-9a-f]{2}/[0-9a-f]{64}-[ad]")
EXECUTABLE = re.compile(r"\w[\w.-]*", re.ASCII)
LIMIT = 512 * 1024 * 1024


def object_file(path):
    info = path.lstat()
    if stat.S_ISDIR(info.st_mode) and re.fullmatch(r"[0-9a-f]{64}-d", path.name):
        # Go stores cached go tool/go run executables in a one-file directory.
        children = list(path.iterdir())
        if len(children) != 1 or not EXECUTABLE.fullmatch(children[0].name):
            raise ValueError("cache contains an invalid executable directory")
        path = children[0]
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode):
            raise ValueError("cache contains a nonregular executable")
    if not stat.S_ISREG(info.st_mode):
        raise ValueError("cache contains a nonregular object")
    return path, info


def objects(directory):
    if directory.is_symlink():
        raise ValueError("cache directory is a symlink")
    if not directory.exists():
        return {}
    result = {}
    for prefix in directory.iterdir():
        if prefix.is_symlink():
            raise ValueError("cache contains a symlink")
        if not prefix.is_dir() or not re.fullmatch(r"[0-9a-f]{2}", prefix.name):
            continue
        for path in prefix.iterdir():
            relative = path.relative_to(directory).as_posix()
            file, info = object_file(path)
            if OBJECT.fullmatch(relative):
                result[file.relative_to(directory).as_posix()] = info
    return result


def copy_object(source, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, destination, follow_symlinks=False)
    if os.geteuid() == 0:
        try:
            owner = pwd.getpwnam("pwuser")
        except KeyError:
            return
        os.chown(destination.parent, owner.pw_uid, owner.pw_gid)
        os.chown(destination.parent.parent, owner.pw_uid, owner.pw_gid)
        os.chown(destination, owner.pw_uid, owner.pw_gid)


def restore_objects(local, payload, baseline):
    # Record the helper image objects before overlaying the restored delta.
    original = objects(local)
    baseline.write_text(json.dumps(list(original)), encoding="utf-8")
    try:
        restored = objects(payload)
        if sum(info.st_size for info in restored.values()) > LIMIT:
            raise ValueError("cache exceeds its size limit")
        entries = {"/".join(path.split("/")[:2]): path for path in original}
        for path in restored:
            existing = entries.get("/".join(path.split("/")[:2]), path)
            if existing != path:
                raise ValueError("cache entry conflicts with the helper cache")
    except ValueError as error:
        print(f"CI_GO_CACHE ignored: {error}")
        return None
    for relative in restored:
        copy_object(payload / relative, local / relative)
    return len(restored), sum(info.st_size for info in restored.values())


def export_objects(local, payload, baseline):
    original = set(json.loads(baseline.read_text(encoding="utf-8")))
    current = objects(local)
    if payload.is_symlink():
        payload.unlink()
    elif payload.exists():
        shutil.rmtree(payload)
    payload.mkdir()
    count = size = 0
    # Keep the newest objects when the bounded archive cannot hold all work.
    for relative, info in sorted(current.items(), key=lambda item: item[1].st_mtime, reverse=True):
        if relative in original or size + info.st_size > LIMIT:
            continue
        copy_object(local / relative, payload / relative)
        count += 1
        size += info.st_size
    return count, size


def transfer(action):
    started = time.monotonic()
    project = Path(os.environ["CI_PROJECT_DIR"]).resolve(strict=True)
    local = Path(os.environ["GOCACHE"])
    if not local.is_absolute() or local.is_symlink():
        raise ValueError("GOCACHE must be an absolute nonsymlink directory")
    local.mkdir(parents=True, exist_ok=True)
    payload = project / ".cache/ci-go"
    baseline = project / ".cache/ci-go-baseline.json"
    baseline.parent.mkdir(parents=True, exist_ok=True)
    if baseline.parent.is_symlink():
        raise ValueError("project cache directory is a symlink")
    if action == "restore":
        result = restore_objects(local, payload, baseline)
        if result is None:
            return
        count, size = result
    elif action == "export":
        count, size = export_objects(local, payload, baseline)
    else:
        raise ValueError("expected restore or export")
    print(f"CI_GO_CACHE action={action} objects={count} bytes={size} seconds={time.monotonic() - started:.3f}")


if __name__ == "__main__":
    transfer(sys.argv[1])
