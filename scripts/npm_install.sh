#!/usr/bin/env bash
# Reuse only the exact dependency tree already installed in the CI helper.
set -euo pipefail
if [[ ${GITLAB_CI:-} == true && -d /opt/mobile-browser/node_modules ]] &&
   cmp -s package.json /opt/mobile-browser/package.json &&
   cmp -s package-lock.json /opt/mobile-browser/package-lock.json; then
  if [[ -L node_modules && $(readlink node_modules) == /opt/mobile-browser/node_modules ]]; then
    exit 0
  elif [[ ! -e node_modules && ! -L node_modules ]]; then
    ln -s /opt/mobile-browser/node_modules node_modules
    exit 0
  fi
elif [[ ${GITLAB_CI:-} == true && -L node_modules &&
        $(readlink node_modules) == /opt/mobile-browser/node_modules ]]; then
  rm node_modules
fi
npm ci --ignore-scripts
