import os
import re
import subprocess


try:
    manifest = os.environ["COMPOSE_FILE"]
    project = os.environ["COMPOSE_PROJECT"]
    if (not os.path.isabs(manifest) or not os.path.isfile(manifest)
            or not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", project)
            or not os.environ["APP_IMAGE"]):
        raise ValueError("Invalid Compose configuration")
    subprocess.run(["docker", "compose", "--file", manifest,
                    "--project-name", project, "up", "--detach"], check=True)
except Exception:
    raise SystemExit("Compose startup failed. Check configuration and permissions.")
