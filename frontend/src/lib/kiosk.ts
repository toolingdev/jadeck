import { useEffect, useSyncExternalStore } from "react";

// Locked screen (the pulpit iPad): no header, no tab bar, no Edit, and
// the screen stays awake. It belongs to this device only. Hold the
// top-right corner for 3 seconds to unlock.

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
