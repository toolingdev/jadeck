#!/usr/bin/env python3
"""Build a staged-update package from this repo at HEAD.

Output (default <repo>/dist-update/<version>/):

    manifest.json          {app, version, commit, created,
                            files: [{path, sha256, size}, ...]}
    payload/server/**      the server tree (code only)
    payload/web/**         the web tree
    payload/server/VERSION written at package time, listed in the
                           manifest like any other file

Payload include/exclude rules (documented in UPDATE.md):
  * IN:  everything under server/ and web/ at HEAD, except…
  * OUT: __pycache__/ and *.pyc/*.pyo (build residue),
         server/data/** (live data — the preset DB; NEVER packaged),
         server/tests_*.py (the test batteries),
         server/VERSION (regenerated fresh into the payload).

The tree must be clean (commit first) unless --allow-dirty is given:
a package is the build its manifest claims, and a dirty tree would
make that claim a lie. Refusing is the default on purpose.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from datetime import datetime, timezone

APP_NAME = "ptzoptics-webapp"
REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def git(repo: str, *args: str) -> str:
    r = subprocess.run(["git", "-C", repo, *args],
                       capture_output=True, text=True, check=True)
    return r.stdout.strip()


def iter_payload_files(repo: str) -> list[str]:
    """Repo-relative payload paths (forward slashes), sorted."""
    out = []
    for tree in ("server", "web"):
        base = os.path.join(repo, tree)
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = sorted(d for d in dirnames
                                 if d != "__pycache__")
            for fn in sorted(filenames):
                rel = os.path.relpath(os.path.join(dirpath, fn), repo)
                rel = rel.replace(os.sep, "/")
                if fn.endswith((".pyc", ".pyo")):
                    continue
                if rel.startswith("server/data/"):
                    continue
                if tree == "server" and fn.startswith("tests_") \
                        and fn.endswith(".py"):
                    continue
                if rel == "server/VERSION":
                    continue          # regenerated below
                out.append(rel)
    return sorted(out)


def _entry(path: str, data: bytes) -> dict:
    return {"path": path,
            "sha256": hashlib.sha256(data).hexdigest(),
            "size": len(data)}


def build_package(repo: str, out_root: str,
                  allow_dirty: bool = False) -> tuple[dict, str]:
    """Build the package for the repo's HEAD. Returns (manifest,
    package_dir). Raises SystemExit with a plain message on refusal."""
    head = git(repo, "rev-parse", "HEAD")
    short = git(repo, "rev-parse", "--short", "HEAD")
    dirty = git(repo, "status", "--porcelain")
    if dirty and not allow_dirty:
        raise SystemExit(
            "refusing to package a dirty tree — commit first "
            "(or pass --allow-dirty):\n" + dirty)
    created = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    pkg = os.path.join(out_root, short)
    if os.path.exists(pkg):
        shutil.rmtree(pkg)
    payload = os.path.join(pkg, "payload")
    os.makedirs(payload)
    files: list[dict] = []
    total = 0
    for rel in iter_payload_files(repo):
        with open(os.path.join(repo, *rel.split("/")), "rb") as f:
            data = f.read()
        dest = os.path.join(payload, *rel.split("/"))
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        with open(dest, "wb") as f:
            f.write(data)
        files.append(_entry(rel, data))
        total += len(data)
    version_data = json.dumps(
        {"version": short, "commit": head, "created": created},
        indent=2).encode()
    with open(os.path.join(payload, "server", "VERSION"), "wb") as f:
        f.write(version_data)
    files.append(_entry("server/VERSION", version_data))
    total += len(version_data)
    files.sort(key=lambda e: e["path"])
    manifest = {"app": APP_NAME, "version": short, "commit": head,
                "created": created, "files": files}
    with open(os.path.join(pkg, "manifest.json"), "w",
              encoding="utf-8") as f:
        json.dump(manifest, f, indent=2)
    manifest["_totalSize"] = total
    return manifest, pkg


def main():
    ap = argparse.ArgumentParser(
        description="Build a staged-update package from HEAD")
    ap.add_argument("--repo", default=REPO)
    ap.add_argument("--out", default=None,
                    help="output root (default <repo>/dist-update)")
    ap.add_argument("--allow-dirty", action="store_true",
                    help="package even with uncommitted changes "
                         "(the manifest still names HEAD — don't "
                         "ship the result)")
    args = ap.parse_args()
    out_root = args.out or os.path.join(args.repo, "dist-update")
    manifest, pkg = build_package(args.repo, out_root,
                                  allow_dirty=args.allow_dirty)
    total = manifest.pop("_totalSize")
    print(f"version : {manifest['version']}  (commit {manifest['commit']})")
    print(f"files   : {len(manifest['files'])}")
    print(f"size    : {total} bytes ({total / 1024:.1f} KiB)")
    print(f"package : {pkg}")


if __name__ == "__main__":
    main()
