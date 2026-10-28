# Transport verification tools

The scanner image uses the existing Playwright 1.61.1 Noble runtime.
Its manifest digest is `sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48`.
The existing distribution retains its system dependency license notices.

testssl.sh 3.2.4 uses GPL version 2.
Its exact source commit is `97763a411c525720a5f9bd9d2cded416b10f210a`.
The source archive SHA-256 is `6c33f1386f8d8a1cf2f6332a18e183fda8209974775d574e41dab01682cf04eb`.
The image retains the complete source and LICENSE under `/opt/testssl`.
The build removes bundled executable files.
The scanner uses the existing distribution OpenSSL executable.
The image adds Ubuntu Noble `busybox-static=1:1.36.1-6ubuntu3.1`.
It supplies the required `hexdump` and `nslookup` commands under GPL-2.0.
Its notices remain in `/usr/share/doc/busybox-static/copyright`.
The build prohibits upgrades of existing system dependencies.
No host tools are installed.

The fixture uses Caddy 2.11.7 under Apache-2.0.
Its manifest digest is `sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f`.
The Compose, development, and demo launchers use the same digest.

The maintainer approved a Go TCP probe instead of Nmap for issue #50.
Debian Nmap 7.95+dfsg-3 still includes NPSL 0.95 in its source LICENSE.
Its packaging copyright summary does not remove these terms.
The project does not add or redistribute Nmap.
The replacement probe uses only the Go standard library.

Sources:

- https://github.com/testssl/testssl.sh/tree/97763a411c525720a5f9bd9d2cded416b10f210a
- https://github.com/caddyserver/caddy/tree/v2.11.7
- https://sources.debian.org/src/nmap/7.95%2Bdfsg-3/LICENSE
