#!/usr/bin/env python3
"""PTZControl staged-update worker.

Runs DETACHED from the app service (the app copies this file into the
updates folder and launches that copy), so it can stop the service,
swap the code, restart it, and — if the new build doesn't answer —
put the old build back, all without anyone at the keyboard.

Self-contained on purpose: stdlib only, and it imports NOTHING from
the app package. It runs while the app's code is stopped and possibly
half-swapped, so it carries its own copy of the package validation.
The two validators (here and app/updates.py) must be kept in step.

Safety invariants (the owner's design, Nov 7, 2026):
  * The staged package is re-verified — every file's SHA-256 against
    the manifest — BEFORE anything is touched. Detection-time trust
    is never enough; bytes can change between the check and the tap.
  * Replacement is manifest-driven only: files the new manifest
    lists are replaced, files the previous manifest listed but the
    new one doesn't are removed, and NOTHING else on disk is ever
    touched — the preset database and config are untouchable.
  * The current build is backed up first; any failure after the swap
    restores it byte-for-byte and restarts it.
  * Every step is logged to <updates>/updater.log, and the outcome is
    persisted to <updates>/last-result.json for the app to surface.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
import traceback
import urllib.request
from datetime import datetime, timezone

APP_NAME = "ptzoptics-webapp"
ARCHIVE_SUFFIXES = (".zip", ".tar", ".tar.gz", ".tgz", ".gz", ".bz2",
                    ".xz", ".7z", ".rar")
DATA_PREFIX = "server/data/"
FORBIDDEN_SUFFIXES = (".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm")
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
APPLY_DELAY_S = 1.5        # let the /apply response flush + UI switch
STOP_WAIT_S = 30
DOWN_WAIT_S = 10
START_WAIT_S = 30


def now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def ts_compact() -> str:
    return datetime.now().strftime("%Y%m%d-%H%M%S")


# ------------------------------------------------------- validation
# A deliberate duplicate of server/app/updates.py's validator (see the
# module docstring for why). Keep the rules in step.

def _path_problem(p) -> str | None:
    if not isinstance(p, str) or not p:
        return "the manifest lists a file with no path"
    if "\\" in p:
        return f"manifest path {p!r} uses backslashes — not a valid package"
    if p.startswith("/") or re.match(r"^[A-Za-z]:", p):
        return f"manifest path {p!r} is absolute — not a valid package"
    parts = p.split("/")
    if any(seg in ("", ".", "..") for seg in parts):
        return f"manifest path {p!r} tries to leave the payload folder"
    if not (p.startswith("server/") or p.startswith("web/")):
        return (f"manifest path {p!r} is outside the server/ and web/ "
                f"folders — not a valid package")
    if p.startswith(DATA_PREFIX) or p.lower().endswith(FORBIDDEN_SUFFIXES):
        return (f"manifest path {p!r} names a data file — data files are "
                f"never part of an update")
    return None


def _sha256_of(path: str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def validate_package(pkg_dir: str):
    """(manifest, None) or (None, reason) — see app/updates.py."""
    if not os.path.isdir(pkg_dir):
        return None, "no package is staged"
    entries = sorted(os.listdir(pkg_dir))
    if not entries:
        return None, "the staging folder is empty"
    extras = [e for e in entries if e not in ("manifest.json", "payload")]
    if extras:
        e = extras[0]
        if e.lower().endswith(ARCHIVE_SUFFIXES):
            return None, (f"{e} is an archive file — update packages are "
                          f"never archives, so it was ignored")
        return None, (f"unexpected file {e!r} in the staged package — "
                      f"nothing was used")
    if "manifest.json" not in entries:
        return None, ("manifest.json is missing — this folder is not an "
                      "update package")
    payload_dir = os.path.join(pkg_dir, "payload")
    if not os.path.isdir(payload_dir):
        return None, "the payload folder is missing"
    try:
        with open(os.path.join(pkg_dir, "manifest.json"),
                  encoding="utf-8") as f:
            manifest = json.load(f)
    except Exception:
        return None, "manifest.json is not readable JSON"
    if not isinstance(manifest, dict):
        return None, "manifest.json has the wrong shape"
    if manifest.get("app") != APP_NAME:
        return None, "the manifest is for a different app"
    if not isinstance(manifest.get("version"), str) \
            or not manifest["version"]:
        return None, "the manifest names no version"
    if not isinstance(manifest.get("commit"), str) \
            or not manifest["commit"]:
        return None, "the manifest names no commit"
    files = manifest.get("files")
    if not isinstance(files, list) or not files:
        return None, "the manifest lists no files"
    listed: dict[str, dict] = {}
    for entry in files:
        if not isinstance(entry, dict):
            return None, "the manifest has a malformed file entry"
        problem = _path_problem(entry.get("path"))
        if problem:
            return None, problem
        p = entry["path"]
        if p in listed:
            return None, f"the manifest lists {p} twice"
        sha = entry.get("sha256")
        if not isinstance(sha, str) or not _SHA256_RE.match(sha):
            return None, f"the manifest has no valid fingerprint for {p}"
        size = entry.get("size")
        if not isinstance(size, int) or size < 0:
            return None, f"the manifest has no valid size for {p}"
        listed[p] = entry
    found: dict[str, str] = {}
    for dirpath, dirnames, filenames in os.walk(payload_dir):
        for d in dirnames:
            if os.path.islink(os.path.join(dirpath, d)):
                return None, "the payload contains a link — not a package"
        for fn in filenames:
            full = os.path.join(dirpath, fn)
            if os.path.islink(full):
                return None, "the payload contains a link — not a package"
            rel = os.path.relpath(full, payload_dir).replace(os.sep, "/")
            found[rel] = full
    extra = sorted(set(found) - set(listed))
    if extra:
        return None, (f"payload file {extra[0]} is not listed in the "
                      f"manifest — nothing was used")
    missing = sorted(set(listed) - set(found))
    if missing:
        return None, f"payload file {missing[0]} is missing"
    for p, entry in listed.items():
        full = found[p]
        if os.path.getsize(full) != entry["size"]:
            return None, (f"payload file {p} has the wrong size — it does "
                          f"not match its manifest fingerprint")
        if _sha256_of(full) != entry["sha256"]:
            return None, (f"payload file {p} does not match its manifest "
                          f"fingerprint")
    return manifest, None


# ------------------------------------------------------------ worker

class Updater:
    def __init__(self, args):
        self.root = args.install_root
        self.updates = args.updates_dir
        self.pending = os.path.join(self.updates, "pending")
        self.health_url = args.health_url
        self.health_timeout = max(5.0, float(args.health_timeout))
        self.service_name = args.service_name
        mode = (args.svc_mode or "").strip().lower()
        if mode not in ("windows", "command", "none"):
            mode = "windows" if os.name == "nt" else "none"
        self.svc_mode = mode

    # ---------------------------------------------------------- util

    def log(self, msg: str):
        line = f"{now_iso()} updater: {msg}"
        try:
            with open(os.path.join(self.updates, "updater.log"), "a",
                      encoding="utf-8") as f:
                f.write(line + "\n")
        except Exception:
            pass
        print(line, flush=True)

    def _write_json(self, name: str, obj: dict):
        tmp = os.path.join(self.updates, name + ".tmp")
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(obj, f, indent=2)
        os.replace(tmp, os.path.join(self.updates, name))

    def write_result(self, result: str, version: str,
                     reason: str | None = None):
        obj = {"result": result, "version": version, "at": now_iso()}
        if reason:
            obj["reason"] = reason
        self._write_json("last-result.json", obj)

    def clear_state(self):
        try:
            os.remove(os.path.join(self.updates, "state.json"))
        except OSError:
            pass

    def install_path(self, rel: str) -> str:
        return os.path.join(self.root, *rel.split("/"))

    def move_pending(self, parent: str, name: str):
        dest_root = os.path.join(self.updates, parent)
        os.makedirs(dest_root, exist_ok=True)
        dest = os.path.join(dest_root, name)
        if os.path.exists(dest):
            dest = os.path.join(dest_root, f"{name}-{ts_compact()}")
        shutil.move(self.pending, dest)
        self.log(f"pending package moved to {parent}/{os.path.basename(dest)}")
        return dest

    # ------------------------------------------------------- service

    def _sc(self, *a):
        return subprocess.run(["sc", *a], capture_output=True, text=True,
                              timeout=30)

    def _sc_state(self) -> int | None:
        try:
            r = self._sc("query", self.service_name)
        except Exception:
            return None
        m = re.search(r"STATE\s*:\s*(\d+)", r.stdout or "")
        return int(m.group(1)) if m else None

    def svc_stop(self) -> bool:
        if self.svc_mode == "none":
            self.log("service control: none (dev mode) — no stop")
            return True
        if self.svc_mode == "command":
            return self._svc_cmd("PTZ_SVC_STOP_CMD", "stop")
        # windows
        try:
            if self._sc_state() == 1:
                self.log(f"service {self.service_name} already stopped")
                return True
            self._sc("stop", self.service_name)
        except Exception as e:
            self.log(f"sc stop failed: {e}")
            return False
        deadline = time.time() + STOP_WAIT_S
        while time.time() < deadline:
            if self._sc_state() == 1:
                self.log(f"service {self.service_name} stopped")
                return True
            time.sleep(0.5)
        self.log(f"service {self.service_name} did not stop in time")
        return False

    def svc_start(self) -> bool:
        if self.svc_mode == "none":
            self.log("service control: none (dev mode) — no start")
            return True
        if self.svc_mode == "command":
            return self._svc_cmd("PTZ_SVC_START_CMD", "start")
        try:
            if self._sc_state() == 4:
                self.log(f"service {self.service_name} already running")
                return True
            r = self._sc("start", self.service_name)
            if r.returncode not in (0,) and "1056" not in (r.stdout + r.stderr):
                self.log(f"sc start rc={r.returncode}: "
                         f"{(r.stdout + r.stderr).strip()[:200]}")
        except Exception as e:
            self.log(f"sc start failed: {e}")
            return False
        deadline = time.time() + START_WAIT_S
        while time.time() < deadline:
            if self._sc_state() == 4:
                self.log(f"service {self.service_name} running")
                return True
            time.sleep(0.5)
        self.log(f"service {self.service_name} did not reach RUNNING")
        return False

    def _svc_cmd(self, env_name: str, what: str) -> bool:
        cmd = os.environ.get(env_name, "")
        if not cmd:
            self.log(f"{env_name} is not set — cannot {what} the service")
            return False
        self.log(f"service {what} via {env_name}: {cmd}")
        try:
            r = subprocess.run(cmd, shell=True, timeout=60,
                               capture_output=True, text=True)
        except Exception as e:
            self.log(f"service {what} command failed: {e}")
            return False
        if r.returncode != 0:
            self.log(f"service {what} command rc={r.returncode}: "
                     f"{(r.stdout + r.stderr).strip()[:200]}")
            return False
        return True

    # -------------------------------------------------------- health

    def _answers(self) -> bool:
        try:
            with urllib.request.urlopen(self.health_url, timeout=2) as r:
                return r.status == 200
        except Exception:
            return False

    def wait_health(self, timeout: float) -> bool:
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self._answers():
                self.log(f"health check OK at {self.health_url}")
                return True
            time.sleep(1.0)
        self.log(f"no health answer within {timeout:.0f}s")
        return False

    def wait_down(self, timeout: float = DOWN_WAIT_S) -> bool:
        deadline = time.time() + timeout
        while time.time() < deadline:
            if not self._answers():
                return True
            time.sleep(0.5)
        return False

    # ------------------------------------------------------------ run

    def run(self) -> int:
        self.log(f"apply starting (install={self.root} "
                 f"svc={self.svc_mode}/{self.service_name})")
        time.sleep(APPLY_DELAY_S)
        # 1. Re-verify EVERYTHING before touching anything.
        manifest, reason = validate_package(self.pending)
        if manifest is None:
            self.log(f"staged package failed re-verification: {reason}")
            label = "unverified"
            self.move_pending("rejected", f"{label}-{ts_compact()}")
            self.write_result(
                "failed", "",
                f"the staged update failed its safety check: {reason}")
            self.clear_state()
            return 2
        version = manifest["version"]
        new_files = [f["path"] for f in manifest["files"]]
        self.log(f"package verified: version {version}, "
                 f"{len(new_files)} files")
        # 2. Read the current state of the install.
        prev_manifest = None
        prev_path = os.path.join(self.root, ".installed-manifest.json")
        if os.path.isfile(prev_path):
            try:
                with open(prev_path, encoding="utf-8") as f:
                    prev_manifest = json.load(f)
            except Exception:
                prev_manifest = None
        prev_files = {f["path"] for f in prev_manifest["files"]} \
            if prev_manifest else set()
        cur_version = "pre-updater"
        ver_path = os.path.join(self.root, "server", "VERSION")
        if os.path.isfile(ver_path):
            try:
                with open(ver_path, encoding="utf-8") as f:
                    cur_version = str(json.load(f).get("version")
                                      or cur_version)
            except Exception:
                pass
        # 3. Back up everything the swap could touch.
        backup_dir = os.path.join(
            self.updates, "backup", f"{cur_version}-{ts_compact()}")
        os.makedirs(backup_dir, exist_ok=True)
        present: set[str] = set()
        for rel in sorted(set(new_files) | prev_files):
            src = self.install_path(rel)
            if os.path.isfile(src):
                dst = os.path.join(backup_dir, *rel.split("/"))
                os.makedirs(os.path.dirname(dst), exist_ok=True)
                shutil.copyfile(src, dst)
                present.add(rel)
        had_prev_manifest = os.path.isfile(prev_path)
        if had_prev_manifest:
            shutil.copyfile(prev_path, os.path.join(
                backup_dir, ".installed-manifest.json"))
        with open(os.path.join(backup_dir, "backup.json"), "w",
                  encoding="utf-8") as f:
            json.dump({"from_version": cur_version, "to_version": version,
                       "present": sorted(present),
                       "had_installed_manifest": had_prev_manifest,
                       "at": now_iso()}, f, indent=2)
        self.log(f"backup written: {backup_dir} "
                 f"({len(present)} files from version {cur_version})")
        # 4. Stop the service — confirmed down before any file moves.
        if not self.svc_stop():
            self.log("could not stop the service — nothing was changed")
            self.write_result(
                "failed", version,
                "the app service could not be stopped, so nothing "
                "was changed — the update is still staged")
            self.clear_state()
            return 3
        if self.svc_mode != "none" and not self.wait_down():
            self.log("the app still answers after stop — aborting swap")
            self.svc_start()
            self.write_result(
                "failed", version,
                "the app did not stop cleanly, so nothing was changed")
            self.clear_state()
            return 3
        # 5. Swap (manifest-driven only), then start + health-check.
        fail_reason = None
        try:
            payload = os.path.join(self.pending, "payload")
            for rel in new_files:
                dst = self.install_path(rel)
                os.makedirs(os.path.dirname(dst), exist_ok=True)
                shutil.copyfile(os.path.join(payload, *rel.split("/")),
                                dst)
            for rel in sorted(prev_files - set(new_files)):
                dst = self.install_path(rel)
                if os.path.isfile(dst):
                    os.remove(dst)
            with open(prev_path, "w", encoding="utf-8") as f:
                json.dump(manifest, f, indent=2)
            self.log("files swapped")
        except Exception as e:
            fail_reason = f"applying the new files failed: {e}"
        if fail_reason is None:
            if not self.svc_start():
                fail_reason = "the updated app service did not start"
            elif not self.wait_health(self.health_timeout):
                fail_reason = ("the updated app did not answer within "
                               f"{self.health_timeout:.0f} seconds")
        if fail_reason is None:
            self.move_pending("applied", version)
            self.write_result("applied", version)
            self.clear_state()
            self.log(f"update applied: now on version {version}")
            return 0
        # 6. Roll back: restore the backup byte-for-byte, restart,
        #    confirm the old build answers, and say so in the result.
        self.log(f"update failed ({fail_reason}) — rolling back")
        self.svc_stop()
        try:
            for rel in new_files:
                dst = self.install_path(rel)
                if rel in present:
                    src = os.path.join(backup_dir, *rel.split("/"))
                    os.makedirs(os.path.dirname(dst), exist_ok=True)
                    shutil.copyfile(src, dst)
                elif os.path.isfile(dst):
                    os.remove(dst)
            for rel in sorted(present - set(new_files)):
                dst = self.install_path(rel)
                os.makedirs(os.path.dirname(dst), exist_ok=True)
                shutil.copyfile(os.path.join(backup_dir, *rel.split("/")),
                                dst)
            if had_prev_manifest:
                shutil.copyfile(os.path.join(
                    backup_dir, ".installed-manifest.json"), prev_path)
            elif os.path.isfile(prev_path):
                os.remove(prev_path)
            self.log("backup restored")
        except Exception as e:
            self.log(f"ROLLBACK ITSELF FAILED: {e}\n"
                     f"{traceback.format_exc()}")
        back = self.svc_start() and self.wait_health(self.health_timeout)
        self.log("old version answering again" if back else
                 "WARNING: old version did not answer after rollback")
        self.move_pending("rejected", f"{version}-{ts_compact()}")
        self.write_result(
            "rolled_back", version,
            f"{fail_reason} — the previous version was restored")
        self.clear_state()
        return 4


def _spawn_detached(argv: list[str]):
    kwargs = dict(stdin=subprocess.DEVNULL, close_fds=True)
    if os.name == "nt":
        kwargs["creationflags"] = 0x00000008 | 0x00000200
    else:
        kwargs["start_new_session"] = True
    subprocess.Popen(argv, **kwargs)


def main() -> int:
    ap = argparse.ArgumentParser(description="PTZControl update worker")
    ap.add_argument("--install-root", required=True)
    ap.add_argument("--updates-dir", required=True)
    ap.add_argument("--health-url",
                    default="http://127.0.0.1:8080/api/status")
    ap.add_argument("--service-name", default="PTZControlApp")
    ap.add_argument("--svc-mode", default="")
    ap.add_argument("--health-timeout", default="45")
    ap.add_argument("--worker", action="store_true",
                    help="internal: the detached second stage")
    args = ap.parse_args()
    if not args.worker:
        # Stage 1: re-spawn as a fully detached worker and exit at
        # once, so the worker is not in the app process's tree when
        # the service stop arrives.
        _spawn_detached([sys.executable, os.path.abspath(__file__),
                         "--worker",
                         "--install-root", args.install_root,
                         "--updates-dir", args.updates_dir,
                         "--health-url", args.health_url,
                         "--service-name", args.service_name,
                         "--svc-mode", args.svc_mode,
                         "--health-timeout", str(args.health_timeout)])
        return 0
    up = Updater(args)
    try:
        return up.run()
    except Exception:
        up.log("unexpected failure:\n" + traceback.format_exc())
        try:
            up.write_result("failed", "",
                            "the updater hit an unexpected error — "
                            "see updater.log on the PC")
            up.clear_state()
        except Exception:
            pass
        return 9


if __name__ == "__main__":
    sys.exit(main())
