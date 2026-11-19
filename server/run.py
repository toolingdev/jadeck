#!/usr/bin/env python3
"""Launch the PTZOptics web controller.

Real camera:   python3 server/run.py
Mock demo:     python3 server/run.py --mock   (spawns the mock camera too)
"""
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)


def _load_env_file():
    """Load KEY=VALUE lines from the file named by PTZ_ENV_FILE into the
    environment (existing variables win). Deployment secrets — camera
    credentials — live in that file on the host only, never in the repo.
    The service manager points PTZ_ENV_FILE at it."""
    path = os.environ.get("PTZ_ENV_FILE")
    if not path or not os.path.isfile(path):
        return
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, val = line.split("=", 1)
            os.environ.setdefault(key.strip(),
                                  val.strip().strip('"').strip("'"))


def main():
    _load_env_file()
    mock_proc = None
    if "--mock" in sys.argv:
        os.environ["PTZ_MOCK"] = "1"
        mock_proc = subprocess.Popen(
            [sys.executable, os.path.join(HERE, "mock_camera.py")])
    from app.config import Config
    import uvicorn
    cfg = Config.from_env()
    try:
        uvicorn.run("app.main:app", host=cfg.bind_host, port=cfg.bind_port,
                    log_level="info")
    finally:
        if mock_proc is not None:
            mock_proc.terminate()


if __name__ == "__main__":
    main()
