#!/usr/bin/env bash
# A private in-job daemon gives bind mounts the same paths as the test process.
set -euo pipefail
[[ ${GITLAB_CI:-} == true ]] || { echo 'This setup requires a GitLab job pod.' >&2; exit 2; }
[[ $(uname -m) == x86_64 ]]
mkdir -p artifacts
mkdir -p "${CI_JOB_TEMP:?}"
git remote set-url origin "${CI_PROJECT_URL:?}.git"
case ${CI_RUNTIME:-docker} in
  none) exit 0 ;;
  docker)
    export DOCKER_HOST=unix:///var/run/docker.sock
    # The legacy DinD wrapper hides /tmp under tmpfs and moves daemon storage into RAM.
    # The Kubernetes build volume provides disk storage outside the build context.
    container=docker dockerd --host="$DOCKER_HOST" --storage-driver=overlay2 \
      --data-root="${CI_BUILDS_DIR:?}/.durpdeploy-ci-docker" \
      > /tmp/durpdeploy-ci-dockerd.log 2>&1 &
    for _ in {1..60}; do
      if docker info >/dev/null 2>&1; then exit 0; fi
      sleep 1
    done
    tail -100 /tmp/durpdeploy-ci-dockerd.log >&2
    echo 'Private CI Docker daemon did not become ready.' >&2
    exit 1 ;;
  podman)
    # Run the job script with ci_rootless.sh after preparing its writable checkout.
    chown -R pwuser:pwuser "$CI_PROJECT_DIR"
    install -d -m 700 -o pwuser -g pwuser /tmp/durpdeploy-ci-podman
    ;;
  *) echo 'Unknown CI runtime.' >&2; exit 2 ;;
esac
