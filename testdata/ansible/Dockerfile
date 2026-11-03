FROM harbor.durp.info/dockerhub/library/python@sha256:1b668429b3511ab407d8e00648891631b0b1a4d7e15e3ca70f38ab5b91ad4ab4

# Test-only execution image and disposable SSH target. No image publication.
RUN apk add --no-cache \
    libedit=20260508.3.1-r1 \
    openssh-client-common=10.3_p1-r1 \
    openssh-client-default=10.3_p1-r1 \
    openssh-keygen=10.3_p1-r1 \
    openssh-server-common=10.3_p1-r1 \
    openssh-server=10.3_p1-r1 \
    openssh-sftp-server=10.3_p1-r1 \
    openssh=10.3_p1-r1
COPY requirements.txt THIRD_PARTY_NOTICES.txt /opt/ansible-notices/
RUN python -m pip install --no-cache-dir --require-hashes --only-binary=:all: \
    -r /opt/ansible-notices/requirements.txt
LABEL org.opencontainers.image.licenses="GPL-3.0-or-later AND Python-2.0 AND BSD-2-Clause AND BSD-3-Clause AND Apache-2.0 AND MIT AND MIT-0 AND ISC"
USER 65534:65534
