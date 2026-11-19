import { useEffect, useSyncExternalStore } from "react";
import type { CamStatus } from "./api";
import { getCamera, onCameraChange } from "./camera";

// One WebSocket per page: status in, drive vectors out. The server's
// drive protocol is a state, not a stream: send the vector when it
// changes, plus a keepalive every 500 ms while it is non-zero (the
// server stops the camera if it hears nothing for 1.2 s).

export type Drive = { pan: number; tilt: number; zoom: number; speed: number };

type State = { status: CamStatus | null; online: boolean; noCamera: boolean };

let state: State = { status: null, online: false, noCamera: false };
const listeners = new Set<() => void>();
let ws: WebSocket | null = null;
let retry: number | undefined;
let refs = 0;

const drive: Drive = { pan: 0, tilt: 0, zoom: 0, speed: 40 };
let lastSent = "";
let keepalive: number | undefined;

function emit(next: Partial<State>) {
  state = { ...state, ...next };
  listeners.forEach((l) => l());
}

function connect() {
  const cam = getCamera();
  const url = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws/control${cam ? `?cam=${encodeURIComponent(cam)}` : ""}`;
  ws = new WebSocket(url);
  ws.onopen = () => {
    emit({ online: true, noCamera: false });
    lastSent = "";
    flush();
  };
  ws.onmessage = (ev) => {
    try {
      const m = JSON.parse(ev.data);
      if (m.t === "status") {
        const { t: _t, ...st } = m;
        void _t;
        emit({ status: st as CamStatus });
      }
    } catch {
      /* ignore */
    }
  };
  ws.onclose = (ev) => {
    if (ws !== null && ev.target !== ws) return; // a socket we already replaced
    emit({ online: false, noCamera: ev.code === 4004 });
    ws = null;
    if (refs > 0) retry = window.setTimeout(connect, ev.code === 4004 ? 3000 : 1500);
  };
}

function send(obj: unknown) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj));
}

function active() {
  return drive.pan !== 0 || drive.tilt !== 0 || drive.zoom !== 0;
}

function flush(force = false) {
  const key = `${drive.pan},${drive.tilt},${drive.zoom},${drive.speed}`;
  if (!force && key === lastSent) return;
  lastSent = key;
  if (active()) send({ t: "drive", ...drive });
  else send({ t: "stop" });
}

/** Update part of the drive vector (direction and/or speed). */
export function setDrive(p: Partial<Drive>) {
  Object.assign(drive, p);
  flush();
  if (active() && keepalive === undefined) {
    keepalive = window.setInterval(() => flush(true), 500);
  } else if (!active() && keepalive !== undefined) {
    clearInterval(keepalive);
    keepalive = undefined;
  }
}

export function stopDrive() {
  setDrive({ pan: 0, tilt: 0, zoom: 0 });
}

export function getSpeed() {
  return drive.speed;
}

// Switching cameras: stop this one, then talk to the other.
onCameraChange(() => {
  stopDrive();
  emit({ status: null });
  if (!ws) return;
  const old = ws;
  ws = null;
  old.onclose = null;
  old.close();
  clearTimeout(retry);
  if (refs > 0) connect();
});

// Stop on tab hide / page leave: never leave the camera moving.
if (typeof window !== "undefined") {
  window.addEventListener("blur", stopDrive);
  document.addEventListener("visibilitychange", () => document.hidden && stopDrive());
  window.addEventListener("pagehide", stopDrive);
}

export function useControl(): State {
  useEffect(() => {
    refs++;
    if (!ws) connect();
    return () => {
      refs--;
      if (refs === 0) {
        clearTimeout(retry);
        ws?.close();
      }
    };
  }, []);
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => state,
  );
}
