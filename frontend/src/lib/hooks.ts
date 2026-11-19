import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";

/** Poll a GET endpoint. Returns data, error, and a reload function. */
export function usePoll<T>(path: string | null, ms = 3000) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const load = useCallback(async () => {
    if (!path) return;
    try {
      setData(await api<T>(path));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }, [path]);
  useEffect(() => {
    load();
    if (ms > 0) timer.current = window.setInterval(() => !document.hidden && load(), ms);
    return () => clearInterval(timer.current);
  }, [load, ms]);
  return { data, error, reload: load, setData };
}

export function useLocal<T>(key: string, initial: T): [T, (v: T) => void] {
  const [v, setV] = useState<T>(() => {
    try {
      const s = localStorage.getItem(key);
      return s ? (JSON.parse(s) as T) : initial;
    } catch {
      return initial;
    }
  });
  const set = useCallback(
    (nv: T) => {
      setV(nv);
      try {
        localStorage.setItem(key, JSON.stringify(nv));
      } catch {
        /* private mode */
      }
    },
    [key],
  );
  return [v, set];
}

// ------------------------------------------------------- shared polling
// Many widgets read the same endpoints (mixer status, presets, ...). One
// poll per path, shared by every subscriber, keeps the controller quiet.

type Entry = { data: unknown; error: string | null; subs: Set<() => void>; timer?: number; ms: number; inflight?: boolean };
const shared = new Map<string, Entry>();

async function pollOnce(path: string) {
  const e = shared.get(path);
  if (!e || e.inflight) return;
  e.inflight = true;
  try {
    e.data = await api(path);
    e.error = null;
  } catch (err) {
    e.error = (err as Error).message;
  } finally {
    e.inflight = false;
  }
  e.subs.forEach((f) => f());
}

export function refreshShared(path: string) {
  return pollOnce(path);
}

export function useShared<T>(path: string | null, ms = 3000): { data: T | null; error: string | null; reload: () => void } {
  const [, force] = useState(0);
  useEffect(() => {
    if (!path) return;
    let e = shared.get(path);
    if (!e) {
      e = { data: null, error: null, subs: new Set(), ms };
      shared.set(path, e);
    }
    const sub = () => force((n) => n + 1);
    e.subs.add(sub);
    if (e.subs.size === 1) {
      pollOnce(path);
      if (ms > 0) e.timer = window.setInterval(() => !document.hidden && pollOnce(path), ms);
    } else if (e.data !== null) {
      sub();
    }
    return () => {
      e!.subs.delete(sub);
      if (e!.subs.size === 0) {
        clearInterval(e!.timer);
        shared.delete(path);
      }
    };
  }, [path, ms]);
  const e = path ? shared.get(path) : undefined;
  return { data: (e?.data as T) ?? null, error: e?.error ?? null, reload: () => path && pollOnce(path) };
}
