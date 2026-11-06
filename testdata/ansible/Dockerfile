FROM harbor.durp.info/dockerhub/library/python:3.12-alpine@sha256:e6dae589c1c05976188fd3bd019afd091445fc533ed2ea29001beddc7368a9c8

# Test-only execution image and disposable SSH target. No image publication.
RUN apk add --no-cache \
    libedit=20260508.3.1-r1 \
    openssh-client-common=10.3_p1-r2 \
    openssh-client-default=10.3_p1-r2 \
    openssh-keygen=10.3_p1-r2 \
    openssh-server-common=10.3_p1-r2 \
    openssh-server=10.3_p1-r2 \
    openssh-sftp-server=10.3_p1-r2 \
    openssh=10.3_p1-r2
COPY requirements.txt THIRD_PARTY_NOTICES.txt /opt/ansible-notices/
RUN python -m pip install --no-cache-dir --require-hashes --only-binary=:all: \
    -r /opt/ansible-notices/requirements.txt
LABEL org.opencontainers.image.licenses="GPL-3.0-or-later AND Python-2.0 AND BSD-2-Clause AND BSD-3-Clause AND Apache-2.0 AND MIT AND MIT-0 AND ISC AND SSH-OpenSSH"
USER 65534:65534
