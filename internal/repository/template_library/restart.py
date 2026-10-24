import os
import re
import subprocess


try:
    service = os.environ["SERVICE_NAME"]
    if not re.fullmatch(r"\w[\w@.:-]*", service, flags=re.ASCII):
        raise ValueError("Invalid service unit name")
    subprocess.run(["systemctl", "--no-ask-password", "restart", "--", service],
                   check=True)
    subprocess.run(["systemctl", "--no-ask-password", "is-active", "--quiet",
                    "--", service], check=True)
    print("Service is active")
except Exception:
    raise SystemExit("Service restart failed. Check the unit and permissions.")
