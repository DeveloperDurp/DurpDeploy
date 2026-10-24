import os
import subprocess


try:
    source = os.environ.get("FILE_SOURCE")
    if not source:
        source = os.path.join(os.environ["DURPDEPLOY_STAGE_DIR"], "artifact")
    destination = os.environ["INSTALL_PATH"]
    if (not os.path.isabs(source) or not os.path.isfile(source)
            or not os.path.isabs(destination) or destination == "/"):
        raise ValueError("Use absolute file paths and an existing source")
    subprocess.run(["install", "-T", "-m", "0755", "--", source, destination],
                   check=True)
    print("File installed")
except Exception:
    raise SystemExit("File installation failed. Check paths and permissions.")
