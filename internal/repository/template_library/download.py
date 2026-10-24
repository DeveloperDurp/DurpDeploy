import os
import re
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, url):
        return None


def artifact_url(value):
    parsed = urllib.parse.urlsplit(value)
    if (parsed.scheme != "https" or not parsed.hostname
            or parsed.username is not None or parsed.fragment):
        raise ValueError("Invalid artifact URL")
    return parsed


temporary_file = None
try:
    stage = os.environ["DURPDEPLOY_STAGE_DIR"]
    url = os.environ["ARTIFACT_URL"]
    original = artifact_url(url)
    token = os.environ.get("ARTIFACT_TOKEN", "")
    if not re.fullmatch(r"[a-zA-Z0-9._~+/-]*=*", token):
        raise ValueError("Invalid bearer token")

    opener = urllib.request.build_opener(NoRedirect())
    deadline = time.monotonic() + 240
    for redirect in range(6):
        target = artifact_url(url)
        headers = {}
        if token and (target.hostname, target.port or 443) == (
                original.hostname, original.port or 443):
            headers["Authorization"] = "Bearer " + token
        request = urllib.request.Request(url, headers=headers)
        try:
            response = opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            if response.status in (301, 302, 303, 307, 308):
                location = response.headers.get("Location")
                if not location or redirect == 5:
                    raise ValueError("Invalid redirect")
                url = urllib.parse.urljoin(url, location)
                continue
            if not 200 <= response.status < 300:
                raise ValueError("Artifact download requires a 2xx response")
            with tempfile.NamedTemporaryFile(
                    dir=stage, prefix="artifact.", delete=False) as output:
                temporary_file = output.name
                received = 0
                while True:
                    if time.monotonic() > deadline:
                        raise TimeoutError("Artifact download timed out")
                    chunk = response.read(64 * 1024)
                    if not chunk:
                        break
                    output.write(chunk)
                    received += len(chunk)
            expected = response.headers.get("Content-Length")
            if expected is not None and received != int(expected):
                raise ValueError("Incomplete artifact response")
            os.replace(temporary_file, os.path.join(stage, "artifact"))
            temporary_file = None
            print("Artifact downloaded to managed staging")
            break
except Exception:
    # HTTP exceptions can contain URLs or credentials. Keep them out of logs.
    raise SystemExit("Artifact download failed. Check HTTPS, credentials, and staging.")
finally:
    if temporary_file is not None:
        os.unlink(temporary_file)
