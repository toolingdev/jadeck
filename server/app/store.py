"""Preset store: names/order/exposure in SQLite (stdlib only).

Preset *positions* live in the camera's VISCA slots; this store only maps
slots to the names and service order the operators use.
"""
from __future__ import annotations

import json
import os
import sqlite3
import threading

SEED_PRESETS = [
    "Main", "Whole Venue", "Specials", "Piano",
    "Special Piano", "Band", "Whole TV View", "Announcements",
]


class PresetStore:
    def __init__(self, path: str):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        self._db = sqlite3.connect(path, check_same_thread=False)
        self._db.row_factory = sqlite3.Row
        self._lock = threading.Lock()
        with self._lock, self._db:
            self._db.execute("""
                CREATE TABLE IF NOT EXISTS presets (
                    id       INTEGER PRIMARY KEY AUTOINCREMENT,
                    name     TEXT NOT NULL,
                    slot     INTEGER NOT NULL UNIQUE,
                    sort     INTEGER NOT NULL,
                    exposure TEXT
                )""")
            n = self._db.execute("SELECT COUNT(*) FROM presets").fetchone()[0]
            if n == 0:
                for i, name in enumerate(SEED_PRESETS):
                    self._db.execute(
                        "INSERT INTO presets (name, slot, sort) VALUES (?,?,?)",
                        (name, i + 1, i))

    @staticmethod
    def _row(r: sqlite3.Row) -> dict:
        return {
            "id": r["id"], "name": r["name"], "slot": r["slot"],
            "sort": r["sort"],
            "exposure": json.loads(r["exposure"]) if r["exposure"] else None,
        }

    def list(self) -> list[dict]:
        with self._lock:
            rows = self._db.execute(
                "SELECT * FROM presets ORDER BY sort, slot").fetchall()
        return [self._row(r) for r in rows]

    def get(self, pid: int) -> dict | None:
        with self._lock:
            r = self._db.execute(
                "SELECT * FROM presets WHERE id=?", (pid,)).fetchone()
        return self._row(r) if r else None

    def next_free_slot(self) -> int | None:
        used = {p["slot"] for p in self.list()}
        for s in range(1, 128):
            if s not in used:
                return s
        return None

    def create(self, name: str, slot: int | None = None,
               exposure: dict | None = None) -> dict:
        if slot is None:
            slot = self.next_free_slot()
        if slot is None:
            raise ValueError("all 127 preset slots are in use")
        with self._lock, self._db:
            mx = self._db.execute(
                "SELECT COALESCE(MAX(sort), -1) FROM presets").fetchone()[0]
            cur = self._db.execute(
                "INSERT INTO presets (name, slot, sort, exposure) "
                "VALUES (?,?,?,?)",
                (name, slot, mx + 1,
                 json.dumps(exposure) if exposure else None))
            pid = cur.lastrowid
        return self.get(pid)

    def update(self, pid: int, name: str | None = None,
               exposure: dict | None = None, set_exposure: bool = False
               ) -> dict | None:
        with self._lock, self._db:
            if name is not None:
                self._db.execute("UPDATE presets SET name=? WHERE id=?",
                                 (name, pid))
            if set_exposure:
                self._db.execute(
                    "UPDATE presets SET exposure=? WHERE id=?",
                    (json.dumps(exposure) if exposure else None, pid))
        return self.get(pid)

    def delete(self, pid: int) -> bool:
        with self._lock, self._db:
            cur = self._db.execute("DELETE FROM presets WHERE id=?", (pid,))
            return cur.rowcount > 0

    def reorder(self, ids: list[int]) -> list[dict]:
        with self._lock, self._db:
            for i, pid in enumerate(ids):
                self._db.execute("UPDATE presets SET sort=? WHERE id=?",
                                 (i, pid))
        return self.list()
