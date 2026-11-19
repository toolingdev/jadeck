#!/usr/bin/env python3
"""Staged-update battery: package validation (hash, structure,
traversal, archives), the packaging tool (dirty-tree refusal,
excludes), then the full apply / corrupt-after-detection / rollback
flows end-to-end against fake install roots whose service is a real
subprocess the updater stops and starts via the command stand-in —
and finally the banner/overlay UI checks over CDP with previews.

Run:  .venv/bin/python server/tests_update.py
Spawns its own services on test ports; touches nothing real.
"""
from __future__ import annotations

import asyncio
import hashlib
import importlib.util
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
import urllib.error
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
from app import updates as upd  # noqa: E402

_spec = importlib.util.spec_from_file_location(
    "make_update_package",
    os.path.join(ROOT, "tools", "make_update_package.py"))
mup = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mup)

CHROME = os.path.expanduser(
    "~/.cache/ms-playwright/chromium-1248/chrome-linux64/chrome")
OLD_VERSION = "oldbuild1"
NEW_VERSION = "newbuild1"

PASS = []
FAIL = []


def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(f"  {'ok  ' if cond else 'FAIL'} {name} {detail}")


def req(method, url, body=None, timeout=30):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method,
                               headers={"Content-Type":
                                        "application/json"})
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:
            return e.code, {}
    except Exception:
        return None, {}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def write_pkg(pkg_dir, files: dict[str, bytes], version=NEW_VERSION,
              commit="c" * 40, extra_root=None):
    """Hand-build a package dir from {relpath: bytes}."""
    payload = os.path.join(pkg_dir, "payload")
    entries = []
    for rel, data in sorted(files.items()):
        dest = os.path.join(payload, *rel.split("/"))
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        with open(dest, "wb") as f:
            f.write(data)
        entries.append({"path": rel, "sha256": sha256(data),
                        "size": len(data)})
    manifest = {"app": "ptzoptics-webapp", "version": version,
                "commit": commit, "created": "2026-11-07T00:00:00Z",
                "files": entries}
    with open(os.path.join(pkg_dir, "manifest.json"), "w") as f:
        json.dump(manifest, f, indent=2)
    if extra_root:
        for name, data in extra_root.items():
            with open(os.path.join(pkg_dir, name), "wb") as f:
                f.write(data)
    return manifest


def fix_manifest_entry(pkg_dir, rel):
    """Recompute one manifest entry after editing its payload file."""
    mpath = os.path.join(pkg_dir, "manifest.json")
    manifest = json.load(open(mpath))
    with open(os.path.join(pkg_dir, "payload", *rel.split("/")),
              "rb") as f:
        data = f.read()
    for e in manifest["files"]:
        if e["path"] == rel:
            e["sha256"] = sha256(data)
            e["size"] = len(data)
    with open(mpath, "w") as f:
        json.dump(manifest, f, indent=2)


BASE_FILES = {"server/run.py": b"print('hi')\n",
              "server/VERSION": b'{"version": "newbuild1"}',
              "web/app.js": b"// app\n",
              "web/index.html": b"<html></html>\n"}


# ------------------------------------------------------------- unit ---

def unit_checks(tmp):
    print("unit: package validation")
    def fresh(name):
        d = os.path.join(tmp, name)
        os.makedirs(d)
        return d

    pkg = fresh("valid")
    write_pkg(pkg, dict(BASE_FILES))
    manifest, reason = upd.validate_package(pkg)
    check("valid package validates", manifest is not None, f"({reason})")
    # status through a real updates-dir layout
    udir = fresh("udir1")
    shutil.copytree(pkg, os.path.join(udir, "pending"))
    st = upd.update_status(tmp, udir)
    check("status available with version + count",
          st["state"] == "available"
          and st["update"]["version"] == NEW_VERSION
          and st["update"]["fileCount"] == len(BASE_FILES),
          f"({st['state']})")

    pkg = fresh("flipped")
    write_pkg(pkg, dict(BASE_FILES))
    p = os.path.join(pkg, "payload", "web", "app.js")
    data = bytearray(open(p, "rb").read())
    data[0] ^= 0xFF
    open(p, "wb").write(bytes(data))
    manifest, reason = upd.validate_package(pkg)
    check("one flipped byte -> invalid, file named",
          manifest is None and "web/app.js" in (reason or ""),
          f"({reason})")

    pkg = fresh("extra")
    write_pkg(pkg, dict(BASE_FILES))
    with open(os.path.join(pkg, "payload", "web", "sneaky.js"),
              "w") as f:
        f.write("// unlisted\n")
    manifest, reason = upd.validate_package(pkg)
    check("extra unlisted file -> invalid",
          manifest is None and "sneaky.js" in (reason or ""),
          f"({reason})")

    pkg = fresh("ziponly")
    write_pkg(pkg, {}, extra_root={"update.zip": b"PK\x03\x04junk"})
    os.remove(os.path.join(pkg, "manifest.json"))
    manifest, reason = upd.validate_package(pkg)
    check("a lone .zip -> not an update",
          manifest is None and reason is not None, f"({reason})")

    pkg = fresh("traversal")
    manifest = write_pkg(pkg, dict(BASE_FILES))
    manifest["files"].append({"path": "../evil.py",
                              "sha256": "0" * 64, "size": 1})
    json.dump(manifest, open(os.path.join(pkg, "manifest.json"), "w"))
    m2, reason = upd.validate_package(pkg)
    check("manifest ../ path -> invalid",
          m2 is None and "leave the payload" in (reason or ""),
          f"({reason})")

    pkg = fresh("backslash")
    manifest = write_pkg(pkg, dict(BASE_FILES))
    manifest["files"][0]["path"] = "server\\run.py"
    json.dump(manifest, open(os.path.join(pkg, "manifest.json"), "w"))
    m2, reason = upd.validate_package(pkg)
    check("manifest backslash path -> invalid",
          m2 is None and "backslash" in (reason or ""), f"({reason})")

    pkg = fresh("datapath")
    manifest = write_pkg(pkg, dict(BASE_FILES))
    manifest["files"].append({"path": "server/data/presets.db",
                              "sha256": "0" * 64, "size": 3})
    json.dump(manifest, open(os.path.join(pkg, "manifest.json"), "w"))
    m2, reason = upd.validate_package(pkg)
    check("manifest naming the preset DB -> invalid",
          m2 is None and "data file" in (reason or ""), f"({reason})")

    pkg = fresh("missing")
    write_pkg(pkg, dict(BASE_FILES))
    os.remove(os.path.join(pkg, "payload", "web", "index.html"))
    manifest, reason = upd.validate_package(pkg)
    check("missing payload file -> invalid",
          manifest is None and "index.html" in (reason or ""),
          f"({reason})")

    try:
        pkg = fresh("symlink")
        write_pkg(pkg, dict(BASE_FILES))
        os.symlink("/etc/hostname",
                   os.path.join(pkg, "payload", "web", "link.txt"))
        manifest, reason = upd.validate_package(pkg)
        check("symlink in payload -> invalid",
              manifest is None and "link" in (reason or ""),
              f"({reason})")
    except (OSError, NotImplementedError):
        print("  skip symlink check (cannot create symlinks)")


def tool_checks(tmp):
    print("packaging tool: temp git repo")
    repo = os.path.join(tmp, "repo")
    os.makedirs(os.path.join(repo, "server", "app"))
    os.makedirs(os.path.join(repo, "server", "data"))
    os.makedirs(os.path.join(repo, "web"))

    def w(rel, data):
        with open(os.path.join(repo, *rel.split("/")), "w") as f:
            f.write(data)
    w(".gitignore", "server/data/\n__pycache__/\n")
    w("server/run.py", "print('x')\n")
    w("server/app/__init__.py", "")
    w("server/tests_demo.py", "# test battery\n")
    w("web/index.html", "<html>v1</html>\n")
    with open(os.path.join(repo, "server", "data", "presets.db"),
              "wb") as f:
        f.write(b"DBBYTES")
    env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
               GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
    subprocess.run(["git", "-C", repo, "init", "-q"], check=True)
    subprocess.run(["git", "-C", repo, "add", "-A"], check=True, env=env)
    subprocess.run(["git", "-C", repo, "commit", "-qm", "one"],
                   check=True, env=env)
    out = os.path.join(tmp, "dist")
    manifest, pkg = mup.build_package(repo, out)
    paths = [f["path"] for f in manifest["files"]]
    check("tool packages the code trees",
          "server/run.py" in paths and "web/index.html" in paths)
    check("tool writes + lists server/VERSION",
          "server/VERSION" in paths
          and os.path.isfile(os.path.join(pkg, "payload", "server",
                                          "VERSION")))
    check("tool excludes tests + data",
          not any("tests_" in p or p.startswith("server/data/")
                  for p in paths), f"({paths})")
    m2, reason = upd.validate_package(pkg)
    check("tool package passes the app validator", m2 is not None,
          f"({reason})")
    w("web/index.html", "<html>v2 dirty</html>\n")
    try:
        mup.build_package(repo, out)
        check("dirty tree refused", False)
    except SystemExit:
        check("dirty tree refused", True)
    manifest, pkg = mup.build_package(repo, out, allow_dirty=True)
    check("--allow-dirty builds anyway", manifest["version"])


# ------------------------------------------------------ integration ---

def copy_install_tree(dst_install):
    """Copy the repo's server+web trees into a fake install root,
    using the packaging include/exclude rules."""
    for rel in mup.iter_payload_files(ROOT):
        src = os.path.join(ROOT, *rel.split("/"))
        dest = os.path.join(dst_install, *rel.split("/"))
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        shutil.copyfile(src, dest)


def build_real_package(pkg_dir, version=NEW_VERSION):
    """A package whose payload is the repo's CURRENT code, stamped
    with a test version — the format the tool produces, built by hand
    so the battery doesn't depend on git state."""
    files = {}
    for rel in mup.iter_payload_files(ROOT):
        with open(os.path.join(ROOT, *rel.split("/")), "rb") as f:
            files[rel] = f.read()
    files["server/VERSION"] = json.dumps(
        {"version": version, "commit": "d" * 40}).encode()
    return write_pkg(pkg_dir, files, version=version, commit="d" * 40)


def tree_hashes(install):
    """sha256 of every payload-side file in the install tree (data
    excluded — it is allowed to live its own life)."""
    out = {}
    for tree in ("server", "web"):
        base = os.path.join(install, tree)
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = [d for d in dirnames if d != "__pycache__"]
            for fn in filenames:
                full = os.path.join(dirpath, fn)
                rel = os.path.relpath(full, install).replace(os.sep, "/")
                if rel.startswith("server/data/"):
                    continue
                with open(full, "rb") as f:
                    out[rel] = sha256(f.read())
    return out


class FakeInstall:
    def __init__(self, base, port, health_timeout=20):
        self.dir = base
        self.install = os.path.join(base, "install")
        self.updates = os.path.join(base, "updates")
        self.port = port
        self.url = f"http://127.0.0.1:{port}"
        os.makedirs(self.updates)
        copy_install_tree(self.install)
        with open(os.path.join(self.install, "server", "VERSION"),
                  "w") as f:
            json.dump({"version": OLD_VERSION, "commit": "0" * 40}, f)
        with open(os.path.join(self.install, "web", "app.js"),
                  "ab") as f:
            f.write(b"\n/* oldbuild marker */\n")
        run_py = os.path.join(self.install, "server", "run.py")
        with open(os.path.join(base, "start.sh"), "w") as f:
            f.write("#!/bin/sh\n"
                    f"export PTZ_PORT={port}\n"
                    "export PTZ_SVC_MODE=command\n"
                    f"export PTZ_SVC_STOP_CMD='sh {base}/stop.sh'\n"
                    f"export PTZ_SVC_START_CMD='sh {base}/start.sh'\n"
                    f"export PTZ_UPDATE_HEALTH_TIMEOUT_S={health_timeout}\n"
                    f"nohup {sys.executable} {run_py} --mock "
                    f">> {base}/svc.log 2>&1 &\n"
                    f"echo $! > {base}/svc.pid\n")
        with open(os.path.join(base, "stop.sh"), "w") as f:
            f.write("#!/bin/sh\n"
                    f"PID=$(cat {base}/svc.pid 2>/dev/null)\n"
                    "[ -n \"$PID\" ] || exit 0\n"
                    "pkill -P $PID 2>/dev/null\n"
                    "kill $PID 2>/dev/null\n"
                    "i=0\n"
                    "while [ $i -lt 40 ]; do kill -0 $PID 2>/dev/null "
                    "|| break; sleep 0.2; i=$((i+1)); done\n"
                    "kill -9 $PID 2>/dev/null\n"
                    "pkill -P $PID 2>/dev/null\n"
                    "exit 0\n")

    def sh(self, script):
        subprocess.run(["sh", os.path.join(self.dir, script)],
                       check=False, timeout=60)

    def start(self):
        self.sh("start.sh")

    def stop(self):
        self.sh("stop.sh")

    def pid(self):
        try:
            return int(open(os.path.join(self.dir, "svc.pid")).read())
        except Exception:
            return None

    def stage(self, pkg_src):
        dest = os.path.join(self.updates, "pending")
        if os.path.exists(dest):
            shutil.rmtree(dest)
        shutil.copytree(pkg_src, dest)
        return dest

    def wait_status(self, timeout=30):
        deadline = time.time() + timeout
        while time.time() < deadline:
            code, s = req("GET", self.url + "/api/update/status",
                          timeout=5)
            if code == 200:
                return s
            time.sleep(0.5)
        return None

    def wait_for(self, pred, timeout, what):
        deadline = time.time() + timeout
        last = None
        while time.time() < deadline:
            code, s = req("GET", self.url + "/api/update/status",
                          timeout=5)
            if code == 200:
                last = s
                if pred(s):
                    return s
            time.sleep(1.0)
        raise AssertionError(f"timed out waiting for {what}: {last}")


def wait_port_free(port=15678, timeout=20):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if socket.socket().connect_ex(("127.0.0.1", port)) != 0:
            return True
        time.sleep(0.3)
    return False


async def _drive_hold(url, seconds, ready):
    import websockets
    async with websockets.connect(url.replace("http", "ws")
                                  + "/ws/control") as ws:
        await ws.send(json.dumps(
            {"t": "drive", "pan": 1, "tilt": 0, "zoom": 0,
             "speed": 40}))
        ready.set()
        await asyncio.sleep(seconds)
        await ws.send(json.dumps(
            {"t": "drive", "pan": 0, "tilt": 0, "zoom": 0,
             "speed": 40}))


def phase_apply(tmp):
    print("integration: full apply against a fake install")
    fake = FakeInstall(os.path.join(tmp, "apply"), 18101)
    try:
        fake.start()
        st = fake.wait_status()
        check("fake service up, pre-updater version read",
              st and st["current"]["version"] == OLD_VERSION,
              f"({st and st['current']})")
        code, _ = req("POST", fake.url + "/api/presets",
                      {"name": "Sentinel"})
        check("sentinel preset stored", code == 201, f"({code})")
        db_path = os.path.join(fake.install, "server", "data",
                               "presets.db")
        with open(db_path, "rb") as f:
            db_before = f.read()
        pkg = os.path.join(tmp, "pkg-good")
        build_real_package(pkg)
        fake.stage(pkg)
        st = fake.wait_status()
        check("staged package detected as available",
              st["state"] == "available"
              and st["update"]["version"] == NEW_VERSION,
              f"({st['state']})")

        # --- apply while driving -> 409
        ready = threading.Event()
        th = threading.Thread(
            target=lambda: asyncio.run(
                _drive_hold(fake.url, 4.0, ready)), daemon=True)
        th.start()
        ready.wait(timeout=10)
        time.sleep(1.0)   # let the control loop latch the drive
        _, s = req("GET", fake.url + "/api/status")
        check("mock drive latched (driving=true)",
              s.get("driving") is True, f"({s.get('driving')})")
        code, res = req("POST", fake.url + "/api/update/apply")
        check("apply while driving -> 409", code == 409, f"({code})")
        th.join(timeout=15)

        # --- the real apply
        pid_before = fake.pid()
        code, res = req("POST", fake.url + "/api/update/apply")
        check("apply accepted", code == 200
              and res.get("state") == "applying", f"({code} {res})")
        st = fake.wait_for(
            lambda s: s["current"]["version"] == NEW_VERSION, 90,
            "new version live")
        check("new version live after apply",
              st["current"]["version"] == NEW_VERSION)
        check("state reads applied", st["state"] == "applied",
              f"({st['state']})")
        with open(os.path.join(ROOT, "web", "app.js"), "rb") as f:
            repo_appjs = f.read()
        with open(os.path.join(fake.install, "web", "app.js"),
                  "rb") as f:
            check("payload file swapped byte-for-byte",
                  f.read() == repo_appjs)
        check("installed manifest recorded",
              os.path.isfile(os.path.join(fake.install,
                                          ".installed-manifest.json")))
        check("pending consumed into applied/",
              not os.path.exists(os.path.join(fake.updates, "pending"))
              and os.path.isdir(os.path.join(fake.updates, "applied",
                                             NEW_VERSION)))
        backups = os.listdir(os.path.join(fake.updates, "backup"))
        bver = None
        if backups:
            with open(os.path.join(fake.updates, "backup", backups[0],
                                   "server", "VERSION")) as f:
                bver = json.load(f)["version"]
        check("backup holds the old version", bver == OLD_VERSION,
              f"({backups})")
        with open(db_path, "rb") as f:
            check("preset DB byte-identical after apply",
                  f.read() == db_before)
        _, presets = req("GET", fake.url + "/api/presets")
        check("sentinel preset still there",
              any(p["name"] == "Sentinel" for p in presets))
        check("service restarted (new pid)",
              fake.pid() not in (None, pid_before),
              f"({pid_before} -> {fake.pid()})")
    finally:
        fake.stop()
        wait_port_free()


def phase_corrupt(tmp):
    print("integration: corrupted after detection -> nothing changes")
    fake = FakeInstall(os.path.join(tmp, "corrupt"), 18102)
    try:
        fake.start()
        st = fake.wait_status()
        check("fake service up", bool(st))
        before = tree_hashes(fake.install)
        pkg = os.path.join(tmp, "pkg-corrupt")
        build_real_package(pkg)
        pending = fake.stage(pkg)
        st = fake.wait_status()
        check("package available before corruption",
              st["state"] == "available")
        pid_before = fake.pid()
        code, _ = req("POST", fake.url + "/api/update/apply")
        check("apply launched", code == 200, f"({code})")
        # Flip a byte NOW — after the apply was authorized, while the
        # detached helper is still in its start delay. Detection-time
        # trust is not enough: the helper's own re-verification must
        # be what stops this, before the service is ever stopped.
        p = os.path.join(pending, "payload", "web", "style.css")
        data = bytearray(open(p, "rb").read())
        data[10] ^= 0xFF
        open(p, "wb").write(bytes(data))
        result = None
        deadline = time.time() + 45
        while time.time() < deadline:
            rp = os.path.join(fake.updates, "last-result.json")
            if os.path.isfile(rp):
                result = json.load(open(rp))
                if result.get("result") == "failed":
                    break
            time.sleep(0.5)
        check("helper re-verification failed the package",
              result and result.get("result") == "failed",
              f"({result})")
        check("reason names the safety check",
              result and "fingerprint" in (result.get("reason") or ""),
              f"({result and result.get('reason')})")
        check("service never restarted", fake.pid() == pid_before)
        s = None
        deadline = time.time() + 20
        while time.time() < deadline:
            code, s = req("GET", fake.url + "/api/update/status")
            if code == 200 and s.get("state") == "failed":
                break
            time.sleep(0.5)
        check("app still answers on the old version",
              s is not None
              and s["current"]["version"] == OLD_VERSION)
        check("status surfaces the failure",
              s is not None and s["state"] == "failed",
              f"({s and s['state']})")
        check("install tree untouched",
              tree_hashes(fake.install) == before)
        check("pending rejected, not left staged",
              not os.path.exists(os.path.join(fake.updates, "pending"))
              and os.listdir(os.path.join(fake.updates, "rejected")))
    finally:
        fake.stop()
        wait_port_free()


def phase_rollback(tmp):
    print("integration: bad new version -> automatic rollback")
    fake = FakeInstall(os.path.join(tmp, "rollback"), 18103,
                       health_timeout=10)
    try:
        fake.start()
        st = fake.wait_status()
        check("fake service up", bool(st))
        before = tree_hashes(fake.install)
        pkg = os.path.join(tmp, "pkg-bad")
        build_real_package(pkg)
        bad = os.path.join(pkg, "payload", "server", "app", "main.py")
        with open(bad, "w") as f:
            f.write("raise RuntimeError('broken build')\n")
        fix_manifest_entry(pkg, "server/app/main.py")
        fake.stage(pkg)
        st = fake.wait_status()
        check("bad package still passes fingerprints (hashes honest)",
              st["state"] == "available", f"({st['state']})")
        code, _ = req("POST", fake.url + "/api/update/apply")
        check("apply launched", code == 200, f"({code})")
        st = fake.wait_for(lambda s: s["state"] == "rolled_back", 150,
                           "rolled_back state")
        check("state reads rolled_back", st["state"] == "rolled_back")
        check("rollback reason recorded",
              bool(st.get("reason")), f"({st.get('reason')})")
        check("back on the old version",
              st["current"]["version"] == OLD_VERSION)
        check("install tree byte-identical after rollback",
              tree_hashes(fake.install) == before)
        check("no installed-manifest left behind",
              not os.path.exists(os.path.join(
                  fake.install, ".installed-manifest.json")))
        code, s = req("GET", fake.url + "/api/status")
        check("old service healthy after rollback", code == 200)
    finally:
        fake.stop()
        wait_port_free()


# ----------------------------------------------------------------- UI ---

async def cdp_checks(tmp):
    print("ui: update banner / updates row / overlay / outcome")
    port = 18199
    env = dict(os.environ, PTZ_PORT=str(port),
               PTZ_DB="/tmp/ptz-update-ui.db",
               PTZ_UPDATES_DIR=os.path.join(tmp, "ui-updates"))
    if os.path.exists("/tmp/ptz-update-ui.db"):
        os.remove("/tmp/ptz-update-ui.db")
    svc = subprocess.Popen(
        [sys.executable, os.path.join(HERE, "run.py"), "--mock"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    SVC = f"http://127.0.0.1:{port}"
    try:
        for _ in range(60):
            code, _ = req("GET", SVC + "/api/status", timeout=3)
            if code == 200:
                break
            time.sleep(0.5)
        dport = 19223
        proc = subprocess.Popen(
            [CHROME, "--headless=new", "--no-sandbox", "--disable-gpu",
             f"--remote-debugging-port={dport}", "about:blank"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            ws_url = None
            for _ in range(50):
                try:
                    rq = urllib.request.Request(
                        f"http://127.0.0.1:{dport}/json/new?url="
                        + urllib.parse.quote(SVC + "/"), method="PUT")
                    with urllib.request.urlopen(rq, timeout=5) as r:
                        ws_url = json.loads(r.read())[
                            "webSocketDebuggerUrl"]
                    break
                except Exception:
                    time.sleep(0.3)
            if not ws_url:
                check("chromium CDP up", False)
                return
            import websockets
            async with websockets.connect(
                    ws_url, max_size=32 * 1024 * 1024) as ws:
                seq = 0

                async def cmd(method, **params):
                    nonlocal seq
                    seq += 1
                    await ws.send(json.dumps(
                        {"id": seq, "method": method,
                         "params": params}))
                    while True:
                        msg = json.loads(await ws.recv())
                        if msg.get("id") == seq:
                            return msg.get("result", {})

                async def ev(expr):
                    r = await cmd("Runtime.evaluate", expression=expr,
                                  returnByValue=True)
                    return r.get("result", {}).get("value")

                await cmd("Page.enable")
                await cmd("Runtime.enable")

                async def shot(url, width, height, path, mobile=False):
                    await cmd("Emulation.setDeviceMetricsOverride",
                              width=width, height=height,
                              deviceScaleFactor=2 if mobile else 1,
                              mobile=mobile)
                    await cmd("Page.navigate", url=url)
                    await asyncio.sleep(4)
                    r = await cmd("Page.captureScreenshot", format="png")
                    import base64
                    with open(path, "wb") as f:
                        f.write(base64.b64decode(r["data"]))

                await shot(SVC + "/?updateDemo=available", 390, 844,
                           os.path.join(
                               ROOT, "ui-preview-mobile-update.png"),
                           mobile=True)
                txt = await ev(
                    "document.getElementById('updateBanner').hidden"
                    " === false && document.getElementById("
                    "'updateBannerText').textContent")
                check("available banner shows on mobile",
                      bool(txt) and "update is ready" in txt, f"({txt})")
                vis = await ev(
                    "document.getElementById('updateApplyBtn')"
                    ".offsetParent !== null")
                check("banner Update button visible", vis is True)

                await shot(SVC + "/?updateDemo=row", 1280, 900,
                           os.path.join(
                               ROOT, "ui-preview-desktop-update.png"))
                row = await ev(
                    "document.getElementById('updState').textContent")
                check("Advanced > Updates row reports the staged build",
                      bool(row) and "ready to install" in row,
                      f"({row})")
                cur = await ev(
                    "document.getElementById('updCurrent').textContent")
                check("Updates row shows current version",
                      cur == "bf7c536", f"({cur})")

                await shot(SVC + "/?updateDemo=applying", 1280, 900,
                           os.path.join(
                               ROOT, "ui-preview-desktop-updating.png"))
                ov = await ev(
                    "document.getElementById('updateOverlay')"
                    ".hidden === false")
                check("updating overlay shows (simulated)", ov is True)

                await shot(SVC + "/?updateDemo=rolledback", 390, 844,
                           os.path.join(
                               ROOT,
                               "ui-preview-mobile-update-rolledback.png"),
                           mobile=True)
                txt = await ev(
                    "document.getElementById('updateBannerText')"
                    ".textContent")
                check("rolled-back outcome banner shows",
                      bool(txt) and "rolled back" in txt, f"({txt})")
                check("preview screenshots written", True)
        finally:
            proc.terminate()
    finally:
        svc.terminate()


def main():
    tmp = tempfile.mkdtemp(prefix="ptzupd-")
    print(f"workdir {tmp}")
    try:
        unit_checks(tmp)
        tool_checks(tmp)
        phase_apply(tmp)
        phase_corrupt(tmp)
        phase_rollback(tmp)
        asyncio.run(cdp_checks(tmp))
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    print(f"\n{len(PASS)} passed, {len(FAIL)} failed"
          + (f" — FAILED: {FAIL}" if FAIL else ""))
    sys.exit(1 if FAIL else 0)


if __name__ == "__main__":
    main()
