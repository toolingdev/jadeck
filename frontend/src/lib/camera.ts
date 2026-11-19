import { useSyncExternalStore } from "react";

// Which camera this screen drives. Each phone/tablet remembers its own
// pick; "" means the main camera. Every API call and the control socket
// carry it, so pages don't have to.

const KEY = "ptz.camera";
let selected = "";
try {
  selected = localStorage.getItem(KEY) ?? "";
} catch {
  /* private mode */
}
const subs = new Set<() => void>();

export function getCamera() {
  return selected;
}

export function setCamera(id: string) {
  if (id === selected) return;
  selected = id;
  try {
    localStorage.setItem(KEY, id);
  } catch {
    /* private mode */
  }
  subs.forEach((f) => f());
}

export function onCameraChange(f: () => void) {
  subs.add(f);
  return () => subs.delete(f);
}

export function useCamera(): string {
  return useSyncExternalStore((l) => {
    subs.add(l);
    return () => subs.delete(l);
  }, getCamera);
}
