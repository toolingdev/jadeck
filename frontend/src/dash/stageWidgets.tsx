import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Check, Send, Eraser, Pause, Play, Square as StopIcon, Plus, WifiOff } from "lucide-react";
import { api, type CameraInfo, type SwitcherStatus, type StreamConfig } from "@/lib/api";
import { useShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { setCamera, useCamera } from "@/lib/camera";
import { cn } from "@/lib/cn";
import { StreamView } from "@/components/StreamView";
import { useAutoSD } from "./videoCount";
import {
  useStage, useScreenName, stageApi, timerLeft, fmtClock, parseTimerPreset, ago, MAX_TEXT,
  type ChannelState, type Message,
} from "@/lib/stage";

type Settings = Record<string, unknown>;
const str = (v: unknown, d = "") => (typeof v === "string" ? v : d);
const bool = (v: unknown, d = false) => (typeof v === "boolean" ? v : d);
const list = (v: unknown, d: string[] = []) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : d);

export const DEFAULT_QUICK = ["5 min left", "Wrap up", "Speak up", "Mic is off", "One more chorus", "Look at camera 2"];
export const DEFAULT_REPLIES = ["👍 Got it", "Ready", "Mic problem", "+2 min"];

function Box({ children, className, style }: { children: ReactNode; className?: string; style?: React.CSSProperties }) {
  return <div style={style} className={cn("flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line bg-panel", className)}>{children}</div>;
}

function Note({ children }: { children: ReactNode }) {
  return <Box><div className="flex h-full items-center justify-center p-3 text-center text-xs text-dim">{children}</div></Box>;
}

function useNow(ms = 1000) {
  const [, set] = useState(0);
  useEffect(() => {
    const t = setInterval(() => set((n) => n + 1), ms);
    return () => clearInterval(t);
  }, [ms]);
}

const hhmm = (at: string) => new Date(at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

// ------------------------------------------------------------- fit text

/**
 * Text as big as the box allows, down to a minimum you can still read
 * from a step away. Past the minimum it scrolls with a finger, with a
 * fade and a "more" hint so nobody misses the rest.
 */
function FitText({ text, className, max = 160 }: { text: string; className?: string; max?: number }) {
  const box = useRef<HTMLDivElement>(null);
  const inner = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState(max);
  const [over, setOver] = useState(false);
  const [atEnd, setAtEnd] = useState(false);

  const fit = () => {
    const b = box.current, i = inner.current;
    if (!b || !i) return;
    const min = Math.max(18, Math.min(30, b.clientHeight * 0.09));
    let lo = min, hi = Math.max(min, Math.min(max, b.clientHeight * 0.8));
    i.style.fontSize = `${hi}px`;
    if (i.scrollHeight <= b.clientHeight && i.scrollWidth <= b.clientWidth) {
      setSize(hi);
      setOver(false);
      return;
    }
    for (let n = 0; n < 12; n++) {
      const mid = (lo + hi) / 2;
      i.style.fontSize = `${mid}px`;
      if (i.scrollHeight <= b.clientHeight && i.scrollWidth <= b.clientWidth) lo = mid;
      else hi = mid;
    }
    i.style.fontSize = `${lo}px`;
    setSize(lo);
    setOver(i.scrollHeight > b.clientHeight + 2);
  };
  useLayoutEffect(fit, [text]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const ro = new ResizeObserver(() => fit());
    if (box.current) ro.observe(box.current);
    return () => ro.disconnect();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    setAtEnd(false);
    box.current?.scrollTo({ top: 0 });
  }, [text]);

  return (
    <div className="relative min-h-0 flex-1">
      <div ref={box} data-feed className={cn("absolute inset-0", over ? "overflow-y-auto overscroll-contain" : "overflow-hidden")}
        onScroll={(e) => {
          const el = e.currentTarget;
          setAtEnd(el.scrollTop + el.clientHeight >= el.scrollHeight - 4);
        }}>
        <div ref={inner} style={{ fontSize: size }} className={cn("whitespace-pre-wrap break-words font-bold leading-[1.15]", className)}>{text}</div>
      </div>
      {over && !atEnd && (
        <div className="pointer-events-none absolute inset-x-0 bottom-0 flex h-16 items-end justify-end bg-gradient-to-t from-panel-2 to-transparent pb-1">
          <span className="rounded-full bg-raise px-2.5 py-1 text-[12px] font-semibold text-muted">↓ more · swipe up</span>
        </div>
      )}
    </div>
  );
}

// ------------------------------------------------------- message display

function ChannelMissing({ id }: { id: string }) {
  return <Note>{id ? `Channel “${id}” doesn't exist any more — pick a channel in this widget's settings.` : "Pick a channel in this widget's settings."}</Note>;
}

export function StageShow({ s, editing }: { s: Settings; editing: boolean }) {
  const id = str(s.channel);
  const { channels, offset, online, loaded } = useStage(id ? [id] : ["-"]);
  const me = useScreenName();
  const ch = channels.find((c) => c.id === id);
  if (!id || (loaded && !ch)) return <ChannelMissing id={id} />;
  if (str(s.style, "big") === "conversation") return <Conversation ch={ch} me={me} offset={offset} online={online} />;
  return <Big ch={ch} me={me} offset={offset} online={online} s={s} editing={editing} />;
}

function Big({ ch, me, offset, online, s, editing }: { ch?: ChannelState; me: string; offset: number; online: boolean; s: Settings; editing: boolean }) {
  useNow(15000);
  const hideOwn = bool(s.hideOwn, true);
  const msgs = (ch?.messages ?? []).filter((m) => !hideOwn || m.from !== me);
  const m = msgs[msgs.length - 1];
  const [pulse, setPulse] = useState(false);
  const last = useRef<string | null>(null);
  useEffect(() => {
    if (!m || m.id === last.current) return;
    const first = last.current === null;
    last.current = m.id;
    if (first) return; // don't flash what was already there on load
    setPulse(true);
    const t = setTimeout(() => setPulse(false), 4000);
    return () => clearTimeout(t);
  }, [m?.id]); // eslint-disable-line react-hooks/exhaustive-deps
  const seen = m?.seen.includes(me);
  const color = ch?.color ?? "#F5A524";
  const empty = str(s.empty, "clock");

  const markSeen = () => {
    if (editing || !m || seen || !bool(s.tapSeen, true)) return;
    stageApi.seen(ch!.id, m.id).catch(() => {});
  };

  return (
    <Box className={cn("bg-panel-2 transition", !online && "opacity-60 grayscale")}
      style={{ borderColor: m ? color : undefined, borderWidth: m ? 3 : 1, animation: pulse ? "stage-pulse 1s ease-in-out 4" : undefined }}>
      <div className="flex h-full flex-col p-[clamp(10px,2.5%,24px)]" onClick={markSeen}>
        <div className="mb-2 flex shrink-0 items-center gap-2 text-[12px] font-bold uppercase tracking-[0.08em] text-muted">
          {m ? (
            <>
              <span className="truncate">{m.from || "Booth"} · {hhmm(m.at)}</span>
              {bool(s.showAge, true) && Date.now() + offset - new Date(m.at).getTime() > 90_000 && <span className="normal-case tracking-normal text-dim">({ago(m.at, offset)})</span>}
            </>
          ) : <span className="truncate">{ch?.name ?? ""}</span>}
          <span className="ml-auto flex items-center gap-1.5">
            {!online && <span className="flex items-center gap-1 normal-case tracking-normal text-bad"><WifiOff size={13} /> offline</span>}
            {m && seen && <span className="flex items-center gap-1 normal-case tracking-normal text-good"><Check size={13} /> seen</span>}
          </span>
        </div>
        {m ? (
          <div className="flex min-h-0 flex-1 flex-col" style={{ color }}><FitText text={m.text} /></div>
        ) : empty === "clock" ? (
          <BigClock />
        ) : empty === "waiting" ? (
          <div className="flex flex-1 items-center justify-center text-sm text-dim">Waiting for a message…</div>
        ) : <div className="flex-1" />}
        {m && !seen && bool(s.tapSeen, true) && <div className="mt-2 shrink-0 text-[13px] text-dim">Tap to mark seen ✓</div>}
      </div>
    </Box>
  );
}

function BigClock() {
  useNow(1000);
  return (
    <div className="flex flex-1 items-center justify-center">
      <span className="font-mono text-[clamp(32px,9vw,96px)] font-bold tabular text-muted">
        {new Date().toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
      </span>
    </div>
  );
}

function Conversation({ ch, me, offset, online }: { ch?: ChannelState; me: string; offset: number; online: boolean }) {
  useNow(30000);
  const list = useRef<HTMLDivElement>(null);
  const msgs = ch?.messages ?? [];
  const lastIn = [...msgs].reverse().find((m) => m.from !== me);
  const [fresh, setFresh] = useState<string | null>(null);
  const prev = useRef<string | null | undefined>(undefined);
  useEffect(() => {
    // Scroll only this widget's list, never the page (scrollIntoView
    // would drag the whole dashboard down to this widget).
    const el = list.current;
    if (el) el.scrollTop = el.scrollHeight;
    const id = lastIn?.id ?? null;
    if (prev.current !== undefined && id && id !== prev.current) {
      setFresh(id);
      const t = setTimeout(() => setFresh(null), 8000);
      prev.current = id;
      return () => clearTimeout(t);
    }
    prev.current = id;
  }, [msgs.length, lastIn?.id]);
  void offset;
  return (
    <Box className={cn(!online && "opacity-70")}>
      <div className="flex shrink-0 items-center gap-2 px-3 pt-2.5 pb-1.5">
        <span className="h-2 w-2 rounded-full" style={{ background: ch?.color }} />
        <span className="truncate text-[12px] font-semibold">{ch?.name ?? "…"} · conversation</span>
        <span className="ml-auto text-[11px] text-dim">{online ? `${ch?.screens.length ?? 0} online` : <span className="text-bad">offline</span>}</span>
      </div>
      <div ref={list} data-feed className="min-h-0 flex-1 space-y-1.5 overflow-y-auto px-3 pb-3">
        {msgs.length === 0 && <p className="pt-4 text-center text-xs text-dim">No messages today.</p>}
        {msgs.map((m) => <Bubble key={m.id} m={m} mine={m.from === me} fresh={m.id === fresh} color={ch?.color} />)}
      </div>
    </Box>
  );
}

function Bubble({ m, mine, fresh, color }: { m: Message; mine: boolean; fresh: boolean; color?: string }) {
  return (
    <div className={cn("flex", mine ? "justify-end" : "justify-start")}>
      <div className={cn("max-w-[85%] rounded-xl border px-3 py-1.5", mine ? "border-line bg-raise" : "bg-[#2a1e08]", fresh && "animate-pulse")}
        style={!mine ? { borderColor: color } : undefined}>
        <div className="text-[10px] text-dim">{hhmm(m.at)} · {mine ? "you" : m.from || "someone"}</div>
        <div className={cn("whitespace-pre-wrap break-words", mine ? "text-sm" : "text-base font-bold")} style={!mine ? { color } : undefined}>{m.text}</div>
        {mine && <div className="text-right text-[10px] text-good">{m.seen.length ? `✓ seen by ${m.seen.join(", ")}` : ""}</div>}
      </div>
    </div>
  );
}

// -------------------------------------------------------- message sender

export function StageSend({ s, editing }: { s: Settings; editing: boolean }) {
  const ids = list(s.channels);
  const { channels, online, loaded } = useStage(ids.length ? ids : ["-"]);
  const me = useScreenName();
  const { log } = useLog();
  const quick = list(s.quick, s.textBox === false ? DEFAULT_REPLIES : DEFAULT_QUICK);
  const timers = list(s.timers);
  const textBox = bool(s.textBox, true);
  const onTap = bool(s.sendOnTap, !textBox);
  const [text, setText] = useState("");
  const [target, setTarget] = useState<string>("all");
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<string | null>(null);
  const live = channels.filter((c) => ids.includes(c.id));
  const targets = target === "all" ? live : live.filter((c) => c.id === target);

  if (!ids.length) return <Note>Pick a channel in this widget's settings.</Note>;
  if (loaded && live.length === 0) return <ChannelMissing id={ids[0]} />;

  const send = async (t: string) => {
    if (editing || !t.trim() || !targets.length) return;
    setBusy(true);
    try {
      await Promise.all(targets.map((c) => stageApi.send(c.id, t)));
      log("Stage", `“${t}” → ${targets.map((c) => c.name).join(", ")}`);
      setFlash(t);
      setTimeout(() => setFlash(null), 1500);
      if (t === text) setText("");
    } catch (e) {
      log("Stage", `Not sent — ${(e as Error).message}`, false);
    } finally {
      setBusy(false);
    }
  };
  const clear = async () => {
    if (editing) return;
    try {
      await Promise.all(targets.map((c) => stageApi.clear(c.id)));
      log("Stage", `Cleared ${targets.map((c) => c.name).join(", ")}`);
    } catch (e) {
      log("Stage", (e as Error).message, false);
    }
  };
  const timer = async (op: string, seconds = 0, label = "") => {
    if (editing) return;
    try {
      await Promise.all(targets.map((c) => stageApi.timer(c.id, op, seconds, label)));
      log("Stage", op === "start" ? `Timer “${label}” ${fmtClock(seconds)}` : `Timer ${op}`);
    } catch (e) {
      log("Stage", (e as Error).message, false);
    }
  };

  // Delivery for my latest message on each target.
  const status = targets.map((c) => {
    const mine = [...c.messages].reverse().find((m) => m.from === me);
    const others = c.screens.filter((x) => x.name !== me);
    if (!mine) return null;
    return { c, mine, line: mine.seen.length ? `✓ Seen on ${mine.seen.join(", ")} · ${hhmm(mine.at)}` : others.length ? `Delivered to ${others.map((x) => x.name).join(", ")}` : "Sent · no other screen on this channel is online" };
  }).filter(Boolean) as { c: ChannelState; mine: Message; line: string }[];

  const long = text.length > 90;
  const anyTimer = targets.some((c) => c.timer);

  return (
    <Box>
      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-3">
        <div className="flex shrink-0 items-center gap-2">
          <span className="truncate text-[12px] font-semibold">{textBox ? "Stage messages" : "Reply"}</span>
          <span className={cn("ml-auto text-[11px]", online ? "text-good" : "text-bad")}>
            {online ? `● ${new Set(live.flatMap((c) => c.screens.filter((x) => x.name !== me).map((x) => x.name))).size} screens online` : "offline"}
          </span>
        </div>
        {live.length > 1 && (
          <div className="flex shrink-0 flex-wrap gap-1 rounded-lg border border-line bg-panel-2 p-1">
            {[{ id: "all", name: "All", color: "" }, ...live].map((c) => (
              <button key={c.id} type="button" onClick={() => setTarget(c.id)}
                className={cn("rounded-md px-2.5 py-1 text-xs", target === c.id ? "bg-accent font-bold text-accent-fg" : "text-muted")}>{c.name}</button>
            ))}
          </div>
        )}
        {textBox && (
          <div className="shrink-0">
            <textarea value={text} maxLength={MAX_TEXT} rows={3} placeholder="Type, paste or dictate…"
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(text); } }}
              className="no-drag w-full resize-none rounded-lg border border-line bg-bg px-3 py-2 text-[15px] outline-none focus:border-accent" />
            <div className="flex items-center gap-2 text-[11px]">
              {long && <span className="text-warn">Long: phones will need a swipe to read it all.</span>}
              <span className="ml-auto text-dim tabular">{text.length} / {MAX_TEXT}</span>
            </div>
          </div>
        )}
        {quick.length > 0 && (
          <div className="grid shrink-0 grid-cols-[repeat(auto-fill,minmax(110px,1fr))] gap-1.5">
            {quick.map((q) => (
              <button key={q} type="button" disabled={busy}
                onClick={() => (onTap ? send(q) : setText(q))}
                className={cn("no-drag min-h-11 rounded-lg border px-2 py-1.5 text-sm font-semibold transition active:scale-[0.97]",
                  flash === q ? "border-good bg-good/15 text-good" : "border-line bg-panel-2 text-fg hover:border-line-strong")}>
                {flash === q ? "✓ Sent" : q}
              </button>
            ))}
          </div>
        )}
        {(textBox || !onTap) && (
          <div className="grid shrink-0 grid-cols-[2fr_1fr] gap-1.5">
            <button type="button" disabled={busy || !text.trim()} onClick={() => send(text)}
              className="no-drag flex h-11 items-center justify-center gap-2 rounded-lg bg-accent text-sm font-bold text-accent-fg disabled:opacity-40">
              <Send size={15} /> {flash && flash === text ? "Sent" : "Send"}
            </button>
            <button type="button" onClick={clear}
              className="no-drag flex h-11 items-center justify-center gap-1.5 rounded-lg border border-line bg-panel-2 text-sm">
              <Eraser size={14} /> Clear screen
            </button>
          </div>
        )}
        {timers.length > 0 && (
          <div className="flex shrink-0 flex-wrap gap-1.5">
            {timers.map((t) => {
              const p = parseTimerPreset(t);
              return p && (
                <button key={t} type="button" onClick={() => timer("start", p.seconds, p.label)}
                  className="no-drag h-9 rounded-lg border border-line bg-panel-2 px-2.5 text-xs font-semibold">⏱ {p.label || "Timer"} {fmtClock(p.seconds)}</button>
              );
            })}
            {anyTimer && (
              <>
                <button type="button" onClick={() => timer("add", 60)} className="no-drag h-9 rounded-lg border border-line bg-panel-2 px-2.5 text-xs"><Plus size={12} className="inline" /> 1 min</button>
                <button type="button" onClick={() => timer("stop")} className="no-drag h-9 rounded-lg border border-line bg-panel-2 px-2.5 text-xs text-bad">Stop timer</button>
              </>
            )}
          </div>
        )}
        {bool(s.showSeen, true) && status.length > 0 && (
          <div className="shrink-0 space-y-0.5 text-[11px]">
            {status.map(({ c, line, mine }) => (
              <div key={c.id} className={mine.seen.length ? "text-good" : "text-muted"}>{live.length > 1 && `${c.name}: `}{line}</div>
            ))}
          </div>
        )}
      </div>
    </Box>
  );
}

// ------------------------------------------------------------------ timer

export function StageTimer({ s, editing }: { s: Settings; editing: boolean }) {
  const id = str(s.channel);
  const { channels, offset, loaded } = useStage(id ? [id] : ["-"]);
  useNow(250);
  const ch = channels.find((c) => c.id === id);
  if (!id || (loaded && !ch)) return <ChannelMissing id={id} />;
  const t = ch?.timer;
  const left = t ? timerLeft(t, offset) : 0;
  const controls = bool(s.controls, false);
  const op = (o: string, sec = 0) => !editing && ch && stageApi.timer(ch.id, o, sec).catch(() => {});
  return (
    <Box className="justify-center">
      <div className="flex h-full items-center gap-3 px-4">
        <div className="min-w-0 flex-1">
          {t ? (
            <div className="flex min-w-0 flex-col">
              <span className="truncate text-[11px] font-bold uppercase tracking-[0.08em] text-muted">
                {t.label || "Timer"} · {left < 0 ? "over" : t.running ? "left" : "paused"}
              </span>
              <span className={cn("font-mono text-[clamp(22px,4.5vw,44px)] font-bold leading-none tabular", left < 0 ? "text-bad" : left <= 120 ? "text-accent" : "text-fg", !t.running && "opacity-60")}>
                {fmtClock(left)}
              </span>
            </div>
          ) : <span className="text-sm text-dim">No timer running</span>}
        </div>
        {controls && t && (
          <div className="flex shrink-0 gap-1">
            <button type="button" className="no-drag flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-panel-2" onClick={() => op(t.running ? "pause" : "resume")} aria-label={t.running ? "Pause" : "Resume"}>{t.running ? <Pause size={15} /> : <Play size={15} />}</button>
            <button type="button" className="no-drag h-9 rounded-lg border border-line bg-panel-2 px-2 text-xs" onClick={() => op("add", 60)}>+1</button>
            <button type="button" className="no-drag flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-panel-2 text-bad" onClick={() => op("stop")} aria-label="Stop"><StopIcon size={13} fill="currentColor" /></button>
          </div>
        )}
      </div>
    </Box>
  );
}

// ------------------------------------------------------------ live status

/** Which vMix input shows this camera: the widget's pick, else the input whose title names the camera. */
export function useTally(cameraId: string, input: number) {
  const { data: sw } = useShared<SwitcherStatus>("/api/switcher/status", 1000);
  const { data: cams } = useShared<CameraInfo[]>("/api/cameras", 10000);
  const header = useCamera();
  return useMemo(() => {
    if (!sw?.connected) return { sw, state: null as null | "pgm" | "pvw" | "off", input: null as number | null };
    let n = input || null;
    if (!n) {
      const id = cameraId || header;
      const cam = cams?.find((c) => c.id === id) ?? cams?.find((c) => c.main);
      const name = cam?.name.toLowerCase().replace(/\s*\(simulated\)/, "").trim();
      if (name) n = sw.inputs.find((i) => i.title.toLowerCase().includes(name) || name.includes(i.title.toLowerCase()))?.number ?? null;
    }
    const state = n == null ? null : n === sw.active ? "pgm" : n === sw.preview ? "pvw" : "off";
    return { sw, state, input: n };
  }, [sw, cams, cameraId, header, input]);
}

export function StageLive({ s }: { s: Settings }) {
  const mode = str(s.mode, "both");
  const { sw, state } = useTally(str(s.camera), Number(s.input) || 0);
  if (!sw) return <Note>Reading the switcher…</Note>;
  if (!sw.connected) return <Note>{sw.label || "vMix"} not connected — live status unknown.</Note>;
  const showTally = mode !== "stream" && state !== null;
  const streaming = sw.streaming;
  let cls = "border-line bg-panel-2 text-muted", text = "Not live";
  if (showTally && state === "pgm") {
    cls = "border-pgm bg-pgm text-white";
    text = streaming || mode === "tally" ? "● WE ARE LIVE · your camera is on" : "● Your camera is on program";
  } else if (showTally && state === "pvw") {
    cls = "border-pvw bg-pvw text-white";
    text = "● NEXT · your camera is up next";
  } else if (mode !== "tally" && streaming) {
    cls = "border-pgm/70 bg-pgm/20 text-bad";
    text = "● WE ARE LIVE";
  } else if (mode === "tally" && state === null) {
    cls = "border-line bg-panel-2 text-dim";
    text = "Camera not found in vMix — pick its input in settings";
  }
  return (
    <div className={cn("flex h-full items-center justify-center rounded-full border-2 px-4 text-center font-bold transition-colors", cls)}>
      <span className="truncate text-[clamp(14px,2.6vw,22px)]">{text}</span>
    </div>
  );
}

// ----------------------------------------------------- camera live view

export function LiveCamPinned({ s, editing }: { s: Settings; editing: boolean }) {
  const pin = str(s.camera);
  const header = useCamera();
  const cfg = useShared<StreamConfig>(pin ? `/api/config?cam=${encodeURIComponent(pin)}` : "/api/config", 0).data;
  const { data: cams } = useShared<CameraInfo[]>("/api/cameras", 10000);
  const quality = str(s.quality, "hd");
  const sd = useAutoSD(quality);
  const { state } = useTally(pin, Number(s.input) || 0);
  const tally = bool(s.tally, false) ? state : null;
  const cam = cams?.find((c) => c.id === (pin || header)) ?? cams?.find((c) => c.main);
  const title = str(s.title) || (pin ? cam?.name : undefined);
  const tapToControl = bool(s.tapToControl, true) && pin && pin !== header && !(cam?.main && header === "");
  return (
    <div className={cn("relative h-full rounded-[var(--radius-card)]", tally === "pgm" && "ring-[3px] ring-pgm", tally === "pvw" && "ring-[3px] ring-pvw")}>
      <StreamView key={`${pin}-${sd ? "sd" : "hd"}`} aspect="h-full"
        title={(title || quality === "auto") && <span className="flex items-center gap-1.5">{tally === "pgm" && <b className="text-pgm">● LIVE</b>}{tally === "pvw" && <b className="text-pvw">● NEXT</b>}{title}{quality === "auto" && <span className="text-dim">{title ? " · " : ""}{sd ? "SD" : "HD"} auto</span>}</span>}
        whep={cfg ? (sd ? cfg.whepSd : cfg.whep) : undefined} hls={cfg ? (sd ? cfg.hlsSd : cfg.hls) : undefined} snapshot={cfg?.snapshot} />
      {tapToControl && !editing && (
        <button type="button" onClick={() => setCamera(cam?.main ? "" : pin)}
          className="absolute bottom-2 left-2 z-10 rounded-lg border border-line bg-bg/80 px-2 py-1 text-[11px] font-semibold text-muted backdrop-blur hover:text-fg">
          Control this camera
        </button>
      )}
    </div>
  );
}

// ------------------------------------------------- pinned one-preset button

export function usePinnedPresets(cam: string) {
  return useShared<{ id: number; name: string; slot: number }[]>(cam ? `/api/presets?cam=${encodeURIComponent(cam)}` : "/api/presets", 15000);
}

export async function recallPinned(cam: string, id: number) {
  return api(`/api/presets/${id}/recall${cam ? `?cam=${encodeURIComponent(cam)}` : ""}`, "POST");
}
