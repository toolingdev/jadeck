"""Staged, hash-verified updates — app side.

The owner's design (Nov 7, 2026): new builds are copied onto the host PC
into an updates folder by whoever ships them; the app only *offers*
an update after every staged file has matched the SHA-256 fingerprint
in the package's manifest. The app never reaches out to any server at
update time, and a random file, zip or tar dropped into the staging
folder is structurally not an update — it is reported as ignored,
never used. Applying is delegated to a detached helper
(`server/updater.py`, copied out of the install tree before launch)
that backs up, swaps manifest-listed files only, health-checks, and
rolls back on its own if the new build doesn't answer.

This module is the read-mostly side: version reporting, package
validation, status, and launching the helper. The helper carries its
own copy of the validator on purpose — it must keep working while
this code is stopped and half-swapped.
"""
from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time

APP_NAME = "ptzoptics-webapp"
ARCHIVE_SUFFIXES = (".zip", ".tar", ".tar.gz", ".tgz", ".gz", ".bz2",
                    ".xz", ".7z", ".rar")
DATA_PREFIX = "server/data/"
FORBIDDEN_SUFFIXES = (".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm")
APPLYING_STALE_S = 900       # an "applying" marker older than this is dead
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def install_root() -> str:
    """The folder that holds server/ and web/ (repo root in dev,
    C:\\PTZControl\\app on the office PC)."""
    return os.path.dirname(os.path.dirname(os.path.dirname(
        os.path.abspath(__file__))))


def default_updates_dir(root: str) -> str:
    return os.path.normpath(os.path.join(root, "..", "updates"))


# ------------------------------------------------------------ version

def current_version(root: str) -> dict:
    """The running build's identity, from server/VERSION (written into
    every package at build time). Builds from before the updater have
    no VERSION file — report that plainly instead of guessing."""
    path = os.path.join(root, "server", "VERSION")
    try:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
        return {"version": str(data.get("version") or "unknown"),
                "commit": str(data.get("commit") or "")}
    except Exception:
        return {"version": "pre-updater build", "commit": ""}


# --------------------------------------------------------- validation

def _path_problem(p) -> str | None:
    """A manifest path must stay inside the payload's code trees.
    Anything else — traversal, absolute paths, the data folder,
    database files — is rejected before a single byte is read."""
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
    """Full structural + fingerprint validation of a staged package.

    Returns (manifest, None) when the package is exactly the build its
    manifest claims, or (None, reason) with a plain-language reason.
    Structure first (layout, manifest shape, path safety), then EVERY
    payload file's size and SHA-256, and any unlisted extra file fails
    the whole package — partial trust is never offered."""
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


# ------------------------------------------------------------- status

def _read_json(path: str):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return None


def read_last_result(updates_dir: str):
    return _read_json(os.path.join(updates_dir, "last-result.json"))


def update_status(root: str, updates_dir: str) -> dict:
    """One of: none | available | invalid | applying | applied |
    rolled_back | failed — plus the details the UI needs."""
    out = {"state": "none",
           "current": current_version(root),
           "update": None,
           "reason": None,
           "result": read_last_result(updates_dir)}
    state = _read_json(os.path.join(updates_dir, "state.json"))
    if state and state.get("state") == "applying":
        try:
            fresh = time.time() - float(state.get("epoch", 0)) \
                < APPLYING_STALE_S
        except Exception:
            fresh = False
        if fresh:
            out["state"] = "applying"
            out["update"] = {"version": state.get("version", "")}
            return out
    pending = os.path.join(updates_dir, "pending")
    if os.path.isdir(pending) and os.listdir(pending):
        manifest, reason = validate_package(pending)
        if manifest is not None:
            files = manifest["files"]
            out["state"] = "available"
            out["update"] = {
                "version": manifest["version"],
                "commit": manifest["commit"],
                "created": manifest.get("created", ""),
                "fileCount": len(files),
                "totalSize": sum(f["size"] for f in files),
            }
        else:
            out["state"] = "invalid"
            out["reason"] = reason
        return out
    result = out["result"]
    if result and result.get("result") in ("applied", "rolled_back",
                                           "failed"):
        out["state"] = result["result"]
        out["reason"] = result.get("reason")
    return out


# -------------------------------------------------------------- apply

class UpdateError(Exception):
    pass


def launch_apply(root: str, updates_dir: str, health_url: str) -> dict:
    """Launch the detached updater helper and return the staged
    manifest. The helper is COPIED out of the install tree first, so
    the file swap it performs can never touch the code it is running,
    and it is spawned detached so it outlives this service's stop."""
    pending = os.path.join(updates_dir, "pending")
    manifest, reason = validate_package(pending)
    if manifest is None:
        raise UpdateError(reason or "no verified update is staged")
    os.makedirs(updates_dir, exist_ok=True)
    src = os.path.join(root, "server", "updater.py")
    if not os.path.isfile(src):
        raise UpdateError("the updater helper is missing from this build")
    run_copy = os.path.join(updates_dir, "updater_run.py")
    try:
        shutil.copyfile(src, run_copy)
    except OSError:
        run_copy = os.path.join(
            updates_dir, f"updater_run-{int(time.time())}.py")
        shutil.copyfile(src, run_copy)
    state = {"state": "applying", "version": manifest["version"],
             "commit": manifest["commit"], "epoch": time.time()}
    with open(os.path.join(updates_dir, "state.json"), "w",
              encoding="utf-8") as f:
        json.dump(state, f, indent=2)
    args = [sys.executable, run_copy,
            "--install-root", root,
            "--updates-dir", updates_dir,
            "--health-url", health_url,
            "--service-name", os.environ.get("PTZ_SERVICE_NAME",
                                             "PTZControlApp"),
            "--svc-mode", os.environ.get("PTZ_SVC_MODE", ""),
            "--health-timeout",
            os.environ.get("PTZ_UPDATE_HEALTH_TIMEOUT_S", "45")]
    log_path = os.path.join(updates_dir, "updater.log")
    with open(log_path, "ab") as logf:
        kwargs = dict(stdin=subprocess.DEVNULL, stdout=logf,
                      stderr=subprocess.STDOUT, close_fds=True)
        if os.name == "nt":
            kwargs["creationflags"] = 0x00000008 | 0x00000200
        else:
            kwargs["start_new_session"] = True
        subprocess.Popen(args, **kwargs)
    return manifest
