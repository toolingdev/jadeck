import { useEffect, useSyncExternalStore } from "react";
import type { CamStatus } from "./api";

// One WebSocket per camera in use: status in, drive vectors out. The
// server's drive protocol is a state, not a stream: send the vector when
// it changes, plus a keepalive every 500 ms while it is non-zero (the
// server stops the camera if it hears nothing for 1.2 s). Each camera
// widget drives its own camera ("" = the main camera).

export type Drive = { pan: number; tilt: number; zoom: number; speed: number };

type State = { status: CamStatus | null; online: boolean; noCamera: boolean };

type Conn = {
  cam: string;
  state: State;
  listeners: Set<() => void>;
  ws: WebSocket | null;
  retry?: number;
  refs: number;
  drive: Drive;
  lastSent: string;
  keepalive?: number;
};

const conns = new Map<string, Conn>();

function conn(cam: string): Conn {
  let c = conns.get(cam);
  if (!c) {
    c = { cam, state: { status: null, online: false, noCamera: false }, listeners: new Set(), ws: null, refs: 0,
      drive: { pan: 0, tilt: 0, zoom: 0, speed: 40 }, lastSent: "" };
    conns.set(cam, c);
  }
  return c;
}

function emit(c: Conn, next: Partial<State>) {
  c.state = { ...c.state, ...next };
  c.listeners.forEach((l) => l());
}

function connect(c: Conn) {
  const url = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws/control${c.cam ? `?cam=${encodeURIComponent(c.cam)}` : ""}`;
  const ws = new WebSocket(url);
  c.ws = ws;
  ws.onopen = () => {
    emit(c, { online: true, noCamera: false });
    c.lastSent = "";
    flush(c);
  };
  ws.onmessage = (ev) => {
    try {
      const m = JSON.parse(ev.data);
      if (m.t === "status") {
        const { t: _t, ...st } = m;
        void _t;
        emit(c, { status: st as CamStatus });
      }
    } catch {
      /* ignore */
    }
  };
  ws.onclose = (ev) => {
    if (c.ws !== ws) return; // a socket we already replaced
    emit(c, { online: false, noCamera: ev.code === 4004 });
    c.ws = null;
    if (c.refs > 0) c.retry = window.setTimeout(() => connect(c), ev.code === 4004 ? 3000 : 1500);
  };
}

function active(c: Conn) {
  return c.drive.pan !== 0 || c.drive.tilt !== 0 || c.drive.zoom !== 0;
}

function flush(c: Conn, force = false) {
  const d = c.drive;
  const key = `${d.pan},${d.tilt},${d.zoom},${d.speed}`;
  if (!force && key === c.lastSent) return;
  c.lastSent = key;
  const msg = active(c) ? { t: "drive", ...d } : { t: "stop" };
  if (c.ws && c.ws.readyState === WebSocket.OPEN) c.ws.send(JSON.stringify(msg));
}

/** Update part of a camera's drive vector (direction and/or speed). */
export function setDrive(p: Partial<Drive>, cam = "") {
  const c = conn(cam);
  Object.assign(c.drive, p);
  flush(c);
  if (active(c) && c.keepalive === undefined) {
    c.keepalive = window.setInterval(() => flush(c, true), 500);
  } else if (!active(c) && c.keepalive !== undefined) {
    clearInterval(c.keepalive);
    c.keepalive = undefined;
  }
}

/** Stop one camera, or every camera when none is named. */
export function stopDrive(cam?: string) {
  for (const c of cam === undefined ? [...conns.values()] : [conn(cam)]) {
    if (active(c) || c.keepalive !== undefined) setDrive({ pan: 0, tilt: 0, zoom: 0 }, c.cam);
  }
}

export function getSpeed(cam = "") {
  return conn(cam).drive.speed;
}

// Stop on tab hide / page leave: never leave a camera moving.
if (typeof window !== "undefined") {
  window.addEventListener("blur", () => stopDrive());
  document.addEventListener("visibilitychange", () => document.hidden && stopDrive());
  window.addEventListener("pagehide", () => stopDrive());
}

export function useControl(cam = ""): State {
  const c = conn(cam);
  useEffect(() => {
    c.refs++;
    if (!c.ws) connect(c);
    return () => {
      c.refs--;
      if (c.refs === 0) {
        stopDrive(c.cam);
        clearTimeout(c.retry);
        const ws = c.ws;
        c.ws = null;
        ws?.close();
      }
    };
  }, [c]);
  return useSyncExternalStore(
    (l) => {
      c.listeners.add(l);
      return () => c.listeners.delete(l);
    },
    () => c.state,
  );
}
