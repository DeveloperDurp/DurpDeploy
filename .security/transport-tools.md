# Transport verification tools

The scanner image uses Ubuntu 26.04.
Its manifest digest is `sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c`.
It installs Debian's `chromium-headless-shell=156.0.8078.79-1~deb13u1` from the security repository.
The image retains package license notices under `/usr/share/doc`.
The browser package retains its notices under `/usr/share/doc/chromium-headless-shell`.

testssl.sh 3.2.4 uses GPL version 2.
Its exact source commit is `97763a411c525720a5f9bd9d2cded416b10f210a`.
The source archive SHA-256 is `6c33f1386f8d8a1cf2f6332a18e183fda8209974775d574e41dab01682cf04eb`.
The image retains the complete source and LICENSE under `/opt/testssl`.
The build removes bundled executable files.
The scanner uses the existing distribution OpenSSL executable.
The image adds Ubuntu Resolute `busybox-static=1:1.37.0-7ubuntu1`.
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
- https://packages.debian.org/trixie/chromium-headless-shell
- https://github.com/caddyserver/caddy/tree/v2.11.7
- https://sources.debian.org/src/nmap/7.95%2Bdfsg-3/LICENSE
