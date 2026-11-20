import { useEffect, useSyncExternalStore } from "react";

// Full screen (the pulpit iPad, a phone at the booth): only the
// dashboard, no header, no tab bar, no Edit, and the screen stays awake.
// It belongs to this device only. The ✕ in the corner (tap twice) or
// leaving the browser's full screen (Esc) ends it.

const KEY = "ptz.locked";
let locked = false;
try {
  locked = localStorage.getItem(KEY) === "1";
} catch {
  /* private mode */
}
const subs = new Set<() => void>();

export function setLocked(v: boolean) {
  locked = v;
  try {
    if (v) localStorage.setItem(KEY, "1");
    else localStorage.removeItem(KEY);
  } catch {
    /* private mode */
  }
  subs.forEach((f) => f());
}

export function useLocked() {
  return useSyncExternalStore((l) => {
    subs.add(l);
    return () => subs.delete(l);
  }, () => locked);
}

type WakeSentinel = { release: () => Promise<void> };

/** Keep the screen on while `on` (iOS 16.4+, recent Android/desktop). */
export function useWakeLock(on: boolean) {
  useEffect(() => {
    if (!on) return;
    const wl = (navigator as Navigator & { wakeLock?: { request: (t: "screen") => Promise<WakeSentinel> } }).wakeLock;
    if (!wl) return;
    let sentinel: WakeSentinel | null = null;
    let gone = false;
    const get = () => {
      if (document.visibilityState !== "visible") return;
      wl.request("screen").then((s) => {
        if (gone) s.release();
        else sentinel = s;
      }).catch(() => {});
    };
    get();
    document.addEventListener("visibilitychange", get); // the lock drops when the tab hides
    return () => {
      gone = true;
      document.removeEventListener("visibilitychange", get);
      sentinel?.release().catch(() => {});
    };
  }, [on]);
}

type FSDoc = Document & { webkitFullscreenElement?: Element; webkitExitFullscreen?: () => Promise<void> };
type FSEl = HTMLElement & { webkitRequestFullscreen?: () => Promise<void> };

/** Can this browser hide its own bars? (Not iPhone Safari.) */
export const canFullScreen = () => {
  const el = document.documentElement as FSEl;
  return !!(el.requestFullscreen || el.webkitRequestFullscreen) && (document.fullscreenEnabled ?? true);
};

/** Opened from the home screen (no browser bars already). */
export const standalone = () =>
  window.matchMedia?.("(display-mode: standalone)").matches || (navigator as Navigator & { standalone?: boolean }).standalone === true;

const inBrowserFS = () => !!(document.fullscreenElement || (document as FSDoc).webkitFullscreenElement);

export function enterFullScreen() {
  setLocked(true);
  const el = document.documentElement as FSEl;
  try {
    const p = el.requestFullscreen ? el.requestFullscreen({ navigationUI: "hide" }) : el.webkitRequestFullscreen?.();
    p?.catch(() => { /* not allowed here: the app still hides its own bars */ });
  } catch { /* ignore */ }
}

export function exitFullScreen() {
  setLocked(false);
  if (inBrowserFS()) {
    const d = document as FSDoc;
    (d.exitFullscreen ? d.exitFullscreen() : d.webkitExitFullscreen?.())?.catch(() => {});
  }
}

// Esc (or the browser's own exit) also leaves our full screen. Only on
// a change: a screen that was full screen before a reload stays put.
// A browser that drops full screen right away (it isn't allowed here)
// doesn't count: the app keeps hiding its own bars.
let fsSince = 0;
for (const ev of ["fullscreenchange", "webkitfullscreenchange"]) {
  document.addEventListener(ev, () => {
    if (inBrowserFS()) fsSince = Date.now();
    else if (locked && fsSince && Date.now() - fsSince > 1000) setLocked(false);
  });
}
