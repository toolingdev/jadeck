import { useEffect, useSyncExternalStore } from "react";
import { api } from "./api";

// Stage messages: one live event stream per set of channels, shared by
// every widget that listens to the same set. The server sends the whole
// state of those channels on every change (it's small), plus its clock,
// so every screen's countdown agrees to the second.

export type Reply = { screen: string; text: string; at: string };
export type Message = { id: string; text: string; from: string; at: string; seen: string[]; replies?: Reply[]; done?: boolean };
export type StageTimer = { label: string; running: boolean; endsAt: string; left: number; total: number };
export type ChannelState = {
  id: string; name: string; color: string; oneAnswer?: boolean;
  messages: Message[]; timer: StageTimer | null; screens: { name: string; online: boolean }[];
};
export type Channel = { id: string; name: string; color: string; oneAnswer?: boolean };
type Snap = { now: string; maxText: number; channels: ChannelState[] };

export const MAX_TEXT = 280;

// ------------------------------------------------------- this screen's name

const NAME_KEY = "ptz.screen";
const nameSubs = new Set<() => void>();

function guessName() {
  const ua = navigator.userAgent;
  if (/iPad|Macintosh/.test(ua) && navigator.maxTouchPoints > 1) return "iPad";
  if (/iPhone/.test(ua)) return "iPhone";
  if (/Android/.test(ua)) return /Mobile/.test(ua) ? "Android phone" : "Android tablet";
  if (/Mac/.test(ua)) return "Mac";
  if (/Windows/.test(ua)) return "Windows PC";
  return "Screen";
}

let screenName = "";
try {
  screenName = localStorage.getItem(NAME_KEY) ?? "";
} catch {
  /* private mode */
}
if (!screenName) screenName = guessName();

export function getScreenName() {
  return screenName;
}

export function setScreenName(n: string) {
  screenName = n.trim().slice(0, 40) || guessName();
  try {
    localStorage.setItem(NAME_KEY, screenName);
  } catch {
    /* private mode */
  }
  nameSubs.forEach((f) => f());
  streams.forEach((s) => s.presence && reconnect(s)); // presence uses the name
}

export function useScreenName() {
  return useSyncExternalStore((l) => {
    nameSubs.add(l);
    return () => nameSubs.delete(l);
  }, getScreenName);
}

// ------------------------------------------------------------ live stream

type Stream = {
  key: string; channels: string[]; presence: boolean; es: EventSource | null; refs: number;
  snap: Snap | null; offset: number; online: boolean; subs: Set<() => void>; view: View;
};
type View = { channels: ChannelState[]; offset: number; online: boolean; loaded: boolean };
const EMPTY: View = { channels: [], offset: 0, online: false, loaded: false };
const streams = new Map<string, Stream>();

function emit(s: Stream) {
  s.view = { channels: s.snap?.channels ?? [], offset: s.offset, online: s.online, loaded: s.snap !== null };
  s.subs.forEach((f) => f());
}

function reconnect(s: Stream) {
  s.es?.close();
  const q = new URLSearchParams(s.presence ? { screen: screenName } : {});
  if (s.channels.length) q.set("channels", s.channels.join(","));
  const es = new EventSource(`/api/stage/events?${q}`);
  s.es = es;
  es.onopen = () => {
    s.online = true;
    emit(s);
  };
  es.onmessage = (ev) => {
    try {
      const snap = JSON.parse(ev.data) as Snap;
      s.snap = snap;
      s.offset = new Date(snap.now).getTime() - Date.now(); // server clock minus ours
      s.online = true;
      emit(s);
    } catch {
      /* ignore */
    }
  };
  // EventSource retries by itself; show "offline" meanwhile.
  es.onerror = () => {
    s.online = false;
    emit(s);
  };
}

/**
 * Live state of these channels ([] = every channel). With presence, this
 * screen shows as online on them (widgets do; pickers and Settings don't).
 */
export function useStage(channels: string[], presence = true): View {
  const key = `${presence ? "p" : "q"}|${[...channels].sort().join(",")}`;
  useEffect(() => {
    let s = streams.get(key);
    if (!s) {
      const list = key.slice(2);
      s = { key, channels: list ? list.split(",") : [], presence, es: null, refs: 0, snap: null, offset: 0, online: false, subs: new Set(), view: EMPTY };
      streams.set(key, s);
      reconnect(s);
    }
    s.refs++;
    const st = s;
    return () => {
      st.refs--;
      if (st.refs === 0) {
        st.es?.close();
        streams.delete(key);
      }
    };
  }, [key]);
  return useSyncExternalStore(
    (l) => {
      const s = streams.get(key);
      s?.subs.add(l);
      return () => s?.subs.delete(l);
    },
    () => streams.get(key)?.view ?? EMPTY,
  );
}

/** All channels (for pickers and Settings), live. */
export function useChannels() {
  return useStage([], false);
}

// ---------------------------------------------------------------- actions

export const stageApi = {
  send: (ch: string, text: string) => api(`/api/stage/channels/${ch}/messages`, "POST", { text, from: screenName }),
  clear: (ch: string) => api(`/api/stage/channels/${ch}/messages`, "DELETE"),
  seen: (ch: string, id: string) => api(`/api/stage/channels/${ch}/seen`, "POST", { id, screen: screenName }),
  reply: (ch: string, id: string, text: string) => api(`/api/stage/channels/${ch}/reply`, "POST", { id, text, screen: screenName }),
  setOneAnswer: (c: Channel, on: boolean) => api<Channel>(`/api/stage/channels/${c.id}`, "PUT", { name: c.name, color: c.color, oneAnswer: on }),
  timer: (ch: string, op: string, seconds = 0, label = "") => api(`/api/stage/channels/${ch}/timer`, "POST", { op, seconds, label }),
  create: (name: string, color: string) => api<Channel>("/api/stage/channels", "POST", { name, color }),
  update: (id: string, name: string, color: string) => api<Channel>(`/api/stage/channels/${id}`, "PUT", { name, color }),
  remove: (id: string) => api(`/api/stage/channels/${id}`, "DELETE"),
};

/** Seconds left on a timer, by the server's clock. */
export function timerLeft(t: StageTimer, offset: number) {
  if (!t.running) return t.left;
  return Math.round((new Date(t.endsAt).getTime() - (Date.now() + offset)) / 1000);
}

export function fmtClock(secs: number) {
  const neg = secs < 0;
  const a = Math.abs(secs);
  const h = Math.floor(a / 3600), m = Math.floor((a % 3600) / 60), s = a % 60;
  return `${neg ? "+" : ""}${h ? `${h}:${String(m).padStart(2, "0")}` : m}:${String(s).padStart(2, "0")}`;
}

/** "Sermon 30" / "Worship 20:00" / "Prayer 2:30" → label + seconds. */
export function parseTimerPreset(line: string): { label: string; seconds: number } | null {
  const m = line.trim().match(/^(.*?)\s*(\d{1,3})(?::(\d{2}))?\s*(?:min|m)?$/i);
  if (!m) return null;
  const seconds = Number(m[2]) * 60 + Number(m[3] ?? 0);
  if (!seconds) return null;
  return { label: m[1].trim(), seconds };
}

export function ago(at: string, offset: number) {
  const s = Math.max(0, Math.round((Date.now() + offset - new Date(at).getTime()) / 1000));
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  return new Date(at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}
