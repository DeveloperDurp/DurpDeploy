import os
import time
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, url):
        return None


try:
    url = os.environ["HEALTH_URL"]
    parsed = urllib.parse.urlsplit(url)
    if (parsed.scheme not in ("http", "https") or not parsed.hostname
            or parsed.username is not None or parsed.fragment):
        raise ValueError("Invalid health URL")
    opener = urllib.request.build_opener(NoRedirect())
    for attempt in range(6):
        try:
            with opener.open(url, timeout=10) as response:
                if 200 <= response.status < 300:
                    print("HTTP health check passed")
                    break
        except Exception:
            pass
        if attempt == 5:
            raise RuntimeError("HTTP health check requires a 2xx response")
        time.sleep(2)
except Exception:
    raise SystemExit("HTTP health check failed. Check the endpoint and network.")
