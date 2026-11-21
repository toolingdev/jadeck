import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { api } from "./api";

/** Sample data for previews (Settings → Add-ons): a widget rendered
 *  inside it reads these instead of the live API, keyed by path without
 *  the query string, and makes no requests. */
export const SampleData = createContext<Record<string, unknown> | null>(null);

function useSample(path: string | null): { hit: boolean; data: unknown } {
  const sample = useContext(SampleData);
  const key = path?.split("?")[0];
  if (sample && key && key in sample) return { hit: true, data: sample[key] };
  return { hit: false, data: null };
}

/** The connection a widget uses ("" = the default one of its kind). */
export const WidgetConn = createContext<string>("");
export const useWidgetConn = () => useContext(WidgetConn);

/** Poll a GET endpoint. Returns data, error, and a reload function. */
export function usePoll<T>(path: string | null, ms = 3000) {
  const sample = useSample(path);
  if (sample.hit) path = null;
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
  if (sample.hit) return { data: sample.data as T, error: null, reload: load, setData };
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

/** Refreshes `path` and every polled variant of it with a query string
 *  (e.g. one add-on state per widget: …/state?widget=…). */
export function refreshSharedAll(path: string) {
  if (!path) return Promise.resolve();
  const hits = [...shared.keys()].filter((k) => k === path || k.startsWith(`${path}?`));
  if (!hits.includes(path)) hits.push(path);
  return Promise.all(hits.map(pollOnce));
}

if (typeof window !== "undefined") {
  window.addEventListener("ptz:changed", () => shared.forEach((_, path) => pollOnce(path)));
}

export function useShared<T>(path: string | null, ms = 3000): { data: T | null; error: string | null; reload: () => void } {
  const sample = useSample(path);
  if (sample.hit) path = null;
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
    const p = path;
    if (e.subs.size === 1) pollOnce(p);
    else if (e.data !== null) sub();
    // poll at the fastest rate any subscriber asked for (one that asked
    // for none must not stop the others from staying current)
    if (ms > 0 && (!e.timer || ms < e.ms)) {
      clearInterval(e.timer);
      e.ms = ms;
      e.timer = window.setInterval(() => !document.hidden && pollOnce(p), ms);
    }
    return () => {
      e!.subs.delete(sub);
      if (e!.subs.size === 0) {
        clearInterval(e!.timer);
        shared.delete(path);
      }
    };
  }, [path, ms]);
  if (sample.hit) return { data: sample.data as T, error: null, reload: () => {} };
  const e = path ? shared.get(path) : undefined;
  return { data: (e?.data as T) ?? null, error: e?.error ?? null, reload: () => path && pollOnce(path) };
}
