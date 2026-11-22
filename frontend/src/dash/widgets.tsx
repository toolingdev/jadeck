import { useEffect, useState, type ReactNode } from "react";
import {
  MonitorPlay, Gamepad2, Bookmark, LayoutGrid, Sun, Home as HomeIcon, Square, ZoomIn, Gauge,
  Tv, Presentation, ChevronsRight, Mic, MicOff, ListMusic, Play, ListChecks, Clock, Timer, Type, ScrollText,
  ChevronLeft, ChevronRight, Loader2, Activity, HeartPulse, MessageSquareText, MessagesSquare, Radio, type LucideIcon,
  Lightbulb,
} from "lucide-react";
import { BandwidthWidget, HealthWidget } from "./metricsWidgets";
import { ComingUp } from "./scheduleWidget";
import { StageShow, StageCue, sendAsCue, showAsCue, StageTimer, StageLive, TallyLight, LiveCamPinned, usePinnedPresets, recallPinned, DEFAULT_QUICK, DEFAULT_REPLIES } from "./stageWidgets";
import { api, type Preset, type Macro, type MixerStatus, type SwitcherStatus, type PresentationStatus, type RunReport, type StreamConfig } from "@/lib/api";
import { useShared, refreshShared, useLocal, useWidgetConn } from "@/lib/hooks";
import { withConn, type Field as AddonField } from "@/lib/addons";
import { useLog } from "@/lib/log";
import { setDrive, stopDrive } from "@/lib/control";
import { useCam } from "@/lib/camera";
import { CamChip } from "@/components/CamChip";
import { cn } from "@/lib/cn";
import { Button, Dialog, Segmented } from "@/components/ui";
import { StreamView } from "@/components/StreamView";
import { refusedText, useFeedRefused } from "@/lib/refused";
import { DirPad, Joystick, HoldButton, SpeedControl, FINE_SPEED, useCamAction } from "@/components/Drive";
import { PresetGrid } from "@/components/Presets";
import { ImageSettings } from "@/components/ImageSettings";
import { Mixer } from "@/components/Booth";
import { Macros } from "@/components/Macros";

// ----------------------------------------------------------------- types

export type Group = "stage" | "camera" | "switcher" | "slides" | "audio" | "addons" | "macros" | "metrics" | "other";
export const GROUPS: { id: Group; label: string; needs?: "switcher" | "presentation" | "mixer" }[] = [
  { id: "stage", label: "Stage" },
  { id: "camera", label: "Camera" },
  { id: "switcher", label: "Switcher", needs: "switcher" },
  { id: "slides", label: "Slides", needs: "presentation" },
  { id: "audio", label: "Audio", needs: "mixer" },
  { id: "addons", label: "Add-ons" },
  { id: "macros", label: "Macros" },
  { id: "metrics", label: "Metrics" },
  { id: "other", label: "Other" },
];

export type Field = {
  key: string; label: string; type: "select" | "bool" | "text" | "number" | "source" | "list" | "channel" | "channels" | "camera" | "input" | "connection" | "feed" | "addon";
  /** connection fields: which connections to offer */
  cap?: "switcher" | "slides" | "mixer"; addon?: string;
  /** type "addon" (v2.1): a field an add-on declared for its widget (WidgetDef.fields). */
  addonField?: AddonField;
  options?: { value: string; label: string }[]; source?: "presets" | "macros" | "favorites" | "slideActions";
  help?: string; showIf?: (s: Settings) => boolean;
};
export type Settings = Record<string, unknown>;
export type WidgetDef = {
  type: string; label: string; group: Group; icon: LucideIcon; description: string;
  size: { w: number; h: number; pw?: number; ph?: number }; // tablet/desktop + phone
  /** Smallest box (px) the content fits in without anything overlapping.
   *  Measured: shrink the widget until something spills out. */
  minPx: { w: number; h: number } | ((s: Settings) => { w: number; h: number });
  defaults: Settings; fields: Field[];
  /** Kept so saved dashboards still render, but not offered in the library. */
  hidden?: boolean;
  /** id / dash: the dashboard widget and dashboard being drawn (absent in previews). */
  Render: (p: { s: Settings; editing: boolean; id?: string; dash?: number }) => ReactNode;
};

const str = (v: unknown, d = "") => (typeof v === "string" ? v : d);
const list = (v: unknown, d: string[] = []) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : d);
const bool = (v: unknown, d = false) => (typeof v === "boolean" ? v : d);

/** Chrome for widgets that aren't already a card. */
export function Frame({ title, children, className, right }: { title?: ReactNode; children: ReactNode; className?: string; right?: ReactNode }) {
  return (
    <div className={cn("card-surface flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line", className)}>
      {title !== undefined && (
        // 13px bold title (WIDGETS.md §6), on the same 18px line as before so
        // nothing below it moves; the context sits right in small caps.
        <div className="flex shrink-0 items-center gap-2 px-3 pt-2.5 pb-1.5">
          <span className="min-w-0 truncate text-[13px] font-bold leading-[18px] tracking-[-0.005em] text-fg [&:has(.no-drag)]:overflow-visible">{title}</span>
          <div className="ml-auto flex items-center gap-2">{right}</div>
        </div>
      )}
      <div className={cn("min-h-0 flex-1", title !== undefined ? "px-3 pb-3" : "p-3")}>{children}</div>
    </div>
  );
}

function Fill({ children }: { children: ReactNode }) {
  // Existing card components: make them fill the cell and scroll inside.
  return <div className="h-full overflow-auto rounded-[var(--radius-card)] [&>section]:min-h-full">{children}</div>;
}

function Unset({ what }: { what: string }) {
  return <Frame><div className="flex h-full items-center justify-center text-center text-xs text-dim">{what}</div></Frame>;
}

function useCfg() {
  return useShared<StreamConfig>("/api/config", 0).data;
}

// ---------------------------------------------------------------- camera

function MoveWidget({ s }: { s: Settings }) {
  const controls = str(s.controls, "switchable") as "switchable" | "buttons" | "joystick";
  const [saved, setSaved] = useLocal<"buttons" | "joystick">("ptz.mode", str(s.opensIn, "buttons") as "buttons" | "joystick");
  const mode = controls === "switchable" ? saved : controls;
  const [speed, setSpeed] = useLocal("ptz.speed", 40);
  const [fine, setFine] = useLocal("ptz.fine", bool(s.startFine));
  const top = fine ? Math.min(FINE_SPEED, speed) : speed;
  const cam = useCam();
  useEffect(() => setDrive({ speed: top }, cam), [top, mode, cam]);
  const home = bool(s.home, true), zoom = bool(s.zoom, true), showSpeed = bool(s.speed, true);
  const act = useCamAction();
  return (
    <Frame title={<span className="flex items-center gap-2">Move <CamChip /></span>} right={controls === "switchable" && (
      <Segmented value={mode} onChange={setSaved} options={[{ value: "buttons", label: "Buttons" }, { value: "joystick", label: "Joystick" }]} />
    )}>
      <div className="flex h-full flex-col gap-3">
        {/* A size container: the pad / stick is a square that fits the
            box's height as well as its width, so it never spills. */}
        <div className="flex min-h-0 flex-1 items-center justify-center gap-3 [container-type:size]">
          {mode === "buttons" ? (
            <DirPad home={home} className={zoom ? "w-[min(300px,100cqh,100cqw-76px)]" : "w-[min(300px,100cqh,100cqw)]"} />
          ) : (
            <div className={cn("flex flex-col items-center gap-2",
              zoom ? "w-[min(280px,100cqh,100cqw-76px)]" : home ? "w-[min(280px,100cqh-52px,100cqw)]" : "w-[min(280px,100cqh,100cqw)]")}>
              <Joystick speed={top} />
              {home && !zoom && (
                <Button className="w-full" onClick={() => act("Home", "/api/ptz/home")}><HomeIcon size={16} /> Home</Button>
              )}
            </div>
          )}
          {zoom && <ZoomCol home={home && mode === "joystick"} />}
        </div>
        {showSpeed && <SpeedControl speed={speed} onChange={setSpeed} fine={fine} onFine={setFine} label={mode === "joystick" ? "Top speed" : "Speed"} />}
      </div>
    </Frame>
  );
}

function ZoomCol({ home }: { home: boolean }) {
  const act = useCamAction();
  const cam = useCam();
  const h = home ? "h-14" : "h-16";
  return (
    <div className="flex shrink-0 flex-col gap-2">
      <HoldButton vec={{ zoom: 1 }} label="Zoom in" className={cn(h, "w-16 flex-col text-[10px] font-semibold")}><ZoomIn size={18} />TELE</HoldButton>
      <HoldButton vec={{ zoom: -1 }} label="Zoom out" className={cn(h, "w-16 flex-col text-[10px] font-semibold")}>−<br />WIDE</HoldButton>
      <button type="button" aria-label="Stop" onClick={() => { stopDrive(cam); act("Stop", "/api/ptz/stop"); }}
        className={cn(h, "btn-pgm flex w-16 items-center justify-center rounded-2xl border transition hover:brightness-110 active:scale-[0.97]")}>
        <Square size={18} fill="currentColor" />
      </button>
      {home && (
        <button type="button" aria-label="Home" onClick={() => act("Home", "/api/ptz/home")}
          className="btn-raised flex h-14 w-16 flex-col items-center justify-center gap-0.5 rounded-2xl border text-[9px] font-bold tracking-[0.1em] text-muted transition hover:brightness-125 active:scale-[0.97]">
          <HomeIcon size={17} className="text-fg" />HOME
        </button>
      )}
    </div>
  );
}

function OnePreset({ s }: { s: Settings }) {
  const cam = str(s.camera);
  const { data } = usePinnedPresets(cam) as { data: Preset[] | null };
  const { log } = useLog();
  const name = str(s.preset);
  const p = data?.find((x) => x.name === name);
  if (!name) return <Unset what="Pick a preset in this widget's settings." />;
  return (
    <button
      onClick={async () => {
        if (!p) return log("Camera", `No preset named “${name}”`, false);
        try { await recallPinned(cam, p.id); log("Camera", `Preset “${p.name}”`); }
        catch (e) { log("Camera", `Preset “${p.name}” failed — ${(e as Error).message}`, false); }
      }}
      className="btn-raised flex h-full w-full flex-col items-start justify-center rounded-[var(--radius-card)] border px-4 text-left transition hover:brightness-125 active:scale-[0.98]"
    >
      <span className="w-full truncate text-lg font-bold">{str(s.label) || name}</span>
      <span className="text-[9px] font-bold uppercase tracking-[0.12em] text-dim">{p ? `PRESET · SLOT ${p.slot}` : "PRESET NOT FOUND"}</span>
    </button>
  );
}

function BigButton({ onClick, className, children, label }: { onClick: () => void; className?: string; children: ReactNode; label: string }) {
  return (
    <button type="button" aria-label={label} onClick={onClick}
      className={cn("flex h-full w-full flex-col items-center justify-center gap-1 rounded-[var(--radius-card)] border text-base font-bold transition active:scale-[0.98]", className)}>
      {children}
    </button>
  );
}

function HomeWidget() {
  const act = useCamAction();
  return <BigButton label="Home" onClick={() => act("Home", "/api/ptz/home")} className="btn-sel hover:brightness-125"><HomeIcon size={26} />Home</BigButton>;
}

function StopWidget() {
  const act = useCamAction();
  const cam = useCam();
  return (
    <BigButton label="Stop camera" onClick={() => { stopDrive(cam); act("Stop", "/api/ptz/stop"); }} className="btn-pgm tracking-[0.1em] hover:brightness-110">
      <Square size={24} fill="currentColor" />STOP CAMERA
    </BigButton>
  );
}

function ZoomWidget() {
  return (
    <Frame>
      <div className="grid h-full grid-rows-2 gap-2">
        <HoldButton vec={{ zoom: 1 }} label="Zoom in" className="h-full w-full flex-col gap-1 text-sm font-bold">+<span>TELE</span></HoldButton>
        <HoldButton vec={{ zoom: -1 }} label="Zoom out" className="h-full w-full flex-col gap-1 text-sm font-bold">−<span>WIDE</span></HoldButton>
      </div>
    </Frame>
  );
}

function SpeedWidget() {
  const [speed, setSpeed] = useLocal("ptz.speed", 40);
  const [fine, setFine] = useLocal("ptz.fine", false);
  return <Frame title="Speed"><SpeedControl speed={speed} onChange={setSpeed} fine={fine} onFine={setFine} /></Frame>;
}

// -------------------------------------------------------------- switcher

function Feed({ kind }: { kind: "program" | "confidence" | "camera"; s: Settings }) {
  const cfg = useCfg();
  const refused = useFeedRefused(kind === "camera" ? undefined : kind);
  const alert = refused ? refusedText(refused, kind) : undefined;
  if (kind === "program") return <StreamView title="Program" whep={cfg?.programWhep} hls={cfg?.programHls} aspect="h-full" noSignalText="The program feed isn't reaching the stream gateway yet." noSignalAlert={alert} />;
  return <StreamView title="Confidence" whep={cfg?.confidenceWhep} hls={cfg?.confidenceHls} aspect="h-full" noSignalText="The confidence feed isn't reaching the gateway yet." noSignalAlert={alert} />;
}

function VideoWidget({ s, editing }: { s: Settings; editing: boolean }) {
  const cfg = useCfg();
  const feed = str(s.feed);
  const refused = useFeedRefused(feed && !feed.startsWith("cam:") ? feed : undefined);
  if (!feed) return <Unset what="Pick a feed in this widget's settings." />;
  // the feed picker sits top-left (where every widget's picker is), so the badges go bottom-left;
  // decided by the setting, not by whether the picker shows, so edit mode looks the same as after Done
  const badges = s.switchHere !== false ? "bottom" : "top";
  if (feed.startsWith("cam:")) return <LiveCamPinned s={{ ...s, camera: feed.slice(4) }} editing={editing} badges={badges} />;
  const f = cfg?.feeds?.find((x) => x.id === feed);
  if (cfg && !f) return <Unset what="That feed was removed — pick another in this widget's settings." />;
  return <StreamView title={str(s.title) || f?.name || ""} whep={f?.whep} hls={f?.hls} aspect="h-full" badges={badges}
    noSignalText={f?.source === "push" ? `Nothing is sending ${f?.name} yet (Settings → Video feeds shows how).` : `${f?.name ?? "This feed"} isn't answering.`}
    noSignalAlert={refused ? refusedText(refused, feed) : undefined} />;
}

function useSwitcher() {
  return useShared<SwitcherStatus>(withConn("/api/switcher/status", useWidgetConn()), 2000);
}

function switchAct(log: ReturnType<typeof useLog>["log"], conn: string) {
  return async (label: string, path: string, body?: unknown) => {
    try { await api(withConn(path, conn), "POST", body); log("Switcher", label); }
    catch (e) { log("Switcher", `${label} failed — ${(e as Error).message}`, false); }
    refreshShared(withConn("/api/switcher/status", conn));
  };
}

function Inputs() {
  const { data: st } = useSwitcher();
  const run = switchAct(useLog().log, useWidgetConn());
  if (!st?.connected) return <Unset what={st?.error || "Switcher not answering."} />;
  return (
    <Frame title={<>Inputs{st.label && <span className="smallcaps ml-2">{st.label}</span>}</>}>
      {/* tiles keep their own height; a big widget leaves room below instead of stretching them tall */}
      <div className="grid h-full auto-rows-[minmax(56px,72px)] content-start grid-cols-[repeat(auto-fill,minmax(120px,1fr))] gap-2 overflow-auto">
        {st.inputs.map((i) => {
          const pgm = i.number === st.active, pvw = i.number === st.preview;
          return (
            <button key={i.key || i.number} onClick={() => run(`Preview input ${i.number}`, "/api/switcher/preview", { input: i.number })}
              className={cn("flex min-h-14 flex-col items-start justify-center rounded-xl border-2 px-3 text-left transition active:scale-[0.98]",
                pgm ? "border-pgm bg-pgm/15 shadow-[var(--shadow-hi),0_0_10px_color-mix(in_srgb,var(--color-pgm)_45%,transparent)]"
                  : pvw ? "border-pvw bg-pvw/12 shadow-[var(--shadow-hi),0_0_10px_color-mix(in_srgb,var(--color-pvw)_40%,transparent)]"
                  : "btn-raised hover:brightness-125")}>
              <span className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted">{i.number} {pgm && <span className="text-pgm">PGM</span>}{pvw && <span className="text-pvw">PVW</span>}</span>
              <span className="w-full truncate text-sm font-semibold">{i.title}</span>
            </button>
          );
        })}
      </div>
    </Frame>
  );
}

function CutFade({ s }: { s: Settings }) {
  const { data: st } = useSwitcher();
  const run = switchAct(useLog().log, useWidgetConn());
  const ms = Number(s.fadeMs) || 1000;
  return (
    <Frame>
      <div className="grid h-full grid-cols-2 gap-2">
        <Button variant="pgm" className="h-full text-xl" disabled={!st?.connected} onClick={() => run(`CUT — input ${st?.preview} to program`, "/api/switcher/cut")}>CUT</Button>
        <Button className="h-full text-lg" disabled={!st?.connected} onClick={() => run(`FADE — input ${st?.preview} to program`, "/api/switcher/fade", { durationMs: ms })}>FADE {ms / 1000}s</Button>
      </div>
    </Frame>
  );
}

// ---------------------------------------------------------------- slides

function useSlides() {
  return useShared<PresentationStatus>(withConn("/api/presentation/status", useWidgetConn()), 5000);
}

function slideAct(log: ReturnType<typeof useLog>["log"], name: string, conn: string) {
  return async (action: string, label: string) => {
    try { await api(withConn(`/api/presentation/${action}`, conn), "POST"); log(name, label); }
    catch (e) { log(name, `${label} failed — ${(e as Error).message}`, false); }
  };
}

function PrevNext() {
  const { data: st } = useSlides();
  const act = slideAct(useLog().log, st?.label || "Slides", useWidgetConn());
  return (
    <Frame>
      <div className="grid h-full grid-cols-2 gap-2">
        <Button className="h-full text-base" disabled={!st?.available} onClick={() => act("prev", "Previous slide")}><ChevronLeft size={20} />Prev</Button>
        <Button className="h-full text-base" disabled={!st?.available} onClick={() => act("next", "Next slide")}>Next<ChevronRight size={20} /></Button>
      </div>
    </Frame>
  );
}

function SlideButton({ s }: { s: Settings }) {
  const { data: st } = useSlides();
  const act = slideAct(useLog().log, st?.label || "Slides", useWidgetConn());
  const action = str(s.action);
  if (!action) return <Unset what="Pick a slide action in this widget's settings." />;
  const label = str(s.label) || { black: "Black screen", logo: "Logo", clear: "Clear text", next: "Next", prev: "Prev" }[action] || action;
  return <BigButton label={label} onClick={() => act(action, label)} className="btn-raised hover:brightness-125">{label}</BigButton>;
}

// ----------------------------------------------------------------- audio

function MuteButton({ s }: { s: Settings }) {
  const conn = useWidgetConn();
  const { data: st } = useShared<MixerStatus>(withConn("/api/mixer/status", conn), 3000);
  const { log } = useLog();
  const [ask, setAsk] = useState(false);
  const [busy, setBusy] = useState(false);
  const slot = Number(s.slot);
  const ch = st?.channels.find((c) => c.slot === slot);
  if (!slot) return <Unset what="Pick a favorite channel in this widget's settings." />;
  if (!ch) return <Unset what={st ? "That favorite no longer exists." : "Reading the mixer…"} />;
  const label = str(s.label) || ch.label;
  const toggle = async (muted: boolean) => {
    setBusy(true);
    try {
      const r = await api<{ muted: boolean }>(withConn("/api/mixer/mute", conn), "POST", { slot, muted });
      log("Audio", `${label} ${r.muted ? "muted" : "live"} (read back)`);
    } catch (e) {
      log("Audio", `${label} — ${(e as Error).message}`, false);
    } finally {
      setBusy(false);
      refreshShared(withConn("/api/mixer/status", conn));
    }
  };
  const press = () => (ch.muted && bool(s.askUnmute) ? setAsk(true) : toggle(!ch.muted));
  return (
    <>
      <button type="button" onClick={press} disabled={busy || ch.channel == null || !st?.connected}
        className={cn("flex h-full w-full flex-col justify-between rounded-[var(--radius-card)] border-2 p-3 text-left transition active:scale-[0.98] disabled:opacity-50",
          // muted = red filled (WIDGETS.md §6)
          ch.muted ? "btn-pgm hover:brightness-110" : "btn-raised hover:brightness-125")}>
        <span className="flex w-full items-start gap-2">
          <span className="min-w-0 flex-1">
            <span className="block truncate text-lg font-bold">{label}</span>
            {bool(s.showChannel, true) && <span className={cn("block text-[11px]", ch.muted ? "text-white/75" : "text-dim")}>{ch.channel ? `Ch ${ch.channel}` : "unassigned"}{bool(s.askUnmute) ? " · asks before unmuting" : ""}</span>}
          </span>
          {busy ? <Loader2 size={18} className="animate-spin text-muted" /> : ch.muted ? <MicOff size={18} className="text-white" /> : <Mic size={18} className="text-good" />}
        </span>
        <span className={cn("rounded-lg py-1.5 text-center text-sm font-bold tracking-[0.12em]", ch.muted ? "bg-black/20 text-white" : ch.muted === false ? "bg-good/15 text-good" : "recess text-dim")}>
          {ch.muted == null ? "—" : ch.muted ? "MUTED" : "LIVE"}
        </span>
      </button>
      <Dialog open={ask} onOpenChange={setAsk} title={`Unmute ${label}?`} description="This channel goes live in the room.">
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setAsk(false)}>Keep muted</Button>
          <Button variant="accent" onClick={() => { setAsk(false); toggle(false); }}>Unmute</Button>
        </div>
      </Dialog>
    </>
  );
}

// ---------------------------------------------------------------- macros

function MacroButton({ s }: { s: Settings }) {
  const { data } = useShared<Macro[]>("/api/macros", 10000);
  const { log } = useLog();
  const [busy, setBusy] = useState(false);
  const name = str(s.macro);
  const m = data?.find((x) => x.name === name);
  if (!name) return <Unset what="Pick a macro in this widget's settings." />;
  const run = async () => {
    if (!m) return log("Macro", `No macro named “${name}”`, false);
    setBusy(true);
    try {
      const r = await api<RunReport>(`/api/macros/${m.id}/run`, "POST");
      log("Macro", r.ok ? `“${m.name}” ran — ${r.steps.length} steps verified` : `“${m.name}” stopped — ${r.error}`, r.ok);
    } catch (e) {
      log("Macro", `“${name}” — ${(e as Error).message}`, false);
    } finally {
      setBusy(false);
      refreshShared("/api/macros");
    }
  };
  return (
    <button type="button" onClick={run} disabled={busy}
      className="btn-raised flex h-full w-full flex-col items-start justify-between rounded-[var(--radius-card)] border p-3 text-left transition hover:brightness-125 active:scale-[0.98]">
      <span className="btn-sel flex h-10 w-10 items-center justify-center rounded-full border">
        {busy ? <Loader2 size={18} className="animate-spin" /> : <Play size={18} fill="currentColor" />}
      </span>
      <span className="w-full">
        <span className="block truncate text-base font-bold">{str(s.label) || name}</span>
        <span className={cn("block text-[11px]", m?.lastRun ? (m.lastRun.ok ? "text-good" : "text-bad") : "text-dim")}>
          {!m ? "macro not found" : m.lastRun ? `last run ${new Date(m.lastRun.finishedAt * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} ${m.lastRun.ok ? "✓" : "✗"}` : `${m.steps.length} steps`}
        </span>
      </span>
    </button>
  );
}

// ----------------------------------------------------------------- other

function useNow(ms = 1000) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

function ClockWidget({ s }: { s: Settings }) {
  const now = useNow();
  const secs = bool(s.seconds);
  return (
    <Frame>
      <div className="flex h-full flex-col items-center justify-center">
        <div className="font-mono text-[clamp(28px,7vw,64px)] font-bold leading-none tabular">
          {now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", ...(secs ? { second: "2-digit" } : {}) })}
        </div>
        {bool(s.date) && <div className="mt-1 text-xs text-muted">{now.toLocaleDateString([], { weekday: "long", month: "short", day: "numeric" })}</div>}
      </div>
    </Frame>
  );
}

function CountdownWidget({ s }: { s: Settings }) {
  const now = useNow();
  const target = str(s.target, "11:00");
  const [hh, mm] = target.split(":").map(Number);
  const t = new Date(now);
  t.setHours(hh || 0, mm || 0, 0, 0);
  const diff = Math.round((t.getTime() - now.getTime()) / 1000);
  const abs = Math.abs(diff);
  const text = `${diff < 0 ? "+" : ""}${Math.floor(abs / 3600) ? `${Math.floor(abs / 3600)}:` : ""}${String(Math.floor((abs % 3600) / 60)).padStart(abs >= 3600 ? 2 : 1, "0")}:${String(abs % 60).padStart(2, "0")}`;
  return (
    <Frame>
      <div className="flex h-full flex-col items-center justify-center">
        <div className="text-[10px] font-bold uppercase leading-4 tracking-[0.12em] text-dim">{str(s.label) || `until ${target}`}</div>
        <div className={cn("font-mono text-[clamp(28px,7vw,64px)] font-bold leading-none tabular", diff < 0 ? "text-bad" : diff < 120 ? "text-accent" : "text-fg")}>{text}</div>
        {diff < 0 && <div className="mt-1 text-[11px] text-bad">over time</div>}
      </div>
    </Frame>
  );
}

function TextWidget({ s }: { s: Settings }) {
  const size = { sm: "text-sm", md: "text-base", lg: "text-2xl", xl: "text-4xl" }[str(s.size, "md")] ?? "text-base";
  return (
    <Frame>
      <div className={cn("flex h-full items-center whitespace-pre-wrap font-semibold", size, str(s.align) === "center" && "justify-center text-center")}>
        {str(s.text) || <span className="text-dim">Write a note in this widget's settings.</span>}
      </div>
    </Frame>
  );
}

function LogWidget() {
  const { entries } = useLog();
  return (
    <Frame title="What just happened">
      {entries.length === 0 ? <p className="text-sm text-dim">Nothing yet this session.</p> : (
        <ul data-feed className="h-full space-y-1 overflow-y-auto font-mono text-xs">
          {entries.map((e) => (
            <li key={e.id} className="flex gap-3">
              <span className="text-dim tabular">{e.at.toLocaleTimeString()}</span>
              <span className={e.ok ? "text-accent" : "text-bad"}>{e.source}</span>
              <span className={e.ok ? "text-fg" : "text-bad"}>{e.text}</span>
            </li>
          ))}
        </ul>
      )}
    </Frame>
  );
}

// -------------------------------------------------------------- registry

const label = (k: string, l: string, extra: Partial<Field> = {}): Field => ({ key: k, label: l, type: "text", ...extra });

const conn = (cap: "switcher" | "slides" | "mixer"): Field => ({ key: "connection", label: "Connection", type: "connection", cap,
  help: "Which one, when you have more than one." });

const CAMERA_FIELD: Field = { key: "camera", label: "Camera", type: "camera", help: "Cameras are added under Settings → Cameras." };

export const WIDGETS: WidgetDef[] = [
  { type: "stage.cue", label: "Cue", group: "stage", icon: MessagesSquare,
    description: "Messages on a stage channel. Up front: the cue shows big with reply buttons — a reply answers it. At the booth: quick messages and a text box, and each screen's answer. Every Cue on the same channel is linked.",
    size: { w: 6, h: 9, pw: 4, ph: 8 },
    minPx: (s) => {
      const inc = bool(s.incoming, true), out = bool(s.outgoing, false);
      let h = 24;
      if (inc) h += 130 + (list(s.replies, DEFAULT_REPLIES).length ? 52 : 0);
      if (out) h += 34 + Math.ceil(list(s.quick, DEFAULT_QUICK).length / 2) * 50 + (bool(s.textBox, false) ? 64 : 0) + 44;
      return { w: 220, h };
    },
    defaults: { channels: ["pulpit"], incoming: true, outgoing: false, replies: DEFAULT_REPLIES, quick: DEFAULT_QUICK, textBox: false, empty: "clock", timers: [] },
    fields: [
      { key: "channels", label: "Channels", type: "channels", help: "Every Cue widget on the same channel, on any screen, is linked." },
      { key: "incoming", label: "Show cues that arrive here", type: "bool", help: "Big, with reply buttons. For the person up front." },
      { key: "replies", label: "Reply buttons", type: "list", showIf: (s) => bool(s.incoming, true), help: "One per line. Tapping one answers the cue for this screen." },
      { key: "empty", label: "When nothing's waiting", type: "select", showIf: (s) => bool(s.incoming, true),
        options: [{ value: "clock", label: "Show the clock" }, { value: "waiting", label: "“Nothing waiting”" }, { value: "blank", label: "Blank" }] },
      { key: "outgoing", label: "Send cues from here", type: "bool", help: "Quick messages, and each screen's answer. For the booth or office." },
      { key: "quick", label: "Quick messages", type: "list", showIf: (s) => bool(s.outgoing, false), help: "One per line. Tapping one sends it." },
      { key: "textBox", label: "Text box (free typing)", type: "bool", showIf: (s) => bool(s.outgoing, false) },
      { key: "timers", label: "Timer buttons", type: "list", showIf: (s) => bool(s.outgoing, false), help: "One per line, e.g. “Sermon 30”. Starts that channel's countdown." },
    ],
    Render: ({ s, editing }) => <StageCue s={s} editing={editing} /> },
  // Older message widgets: kept working (drawn as a Cue), not offered any more.
  { type: "stage.send", hidden: true, label: "Message sender", group: "stage", icon: MessageSquareText,
    description: "Replaced by Cue.", size: { w: 6, h: 9, pw: 4, ph: 9 }, minPx: { w: 220, h: 120 },
    defaults: { channels: ["pulpit"], textBox: true }, fields: [{ key: "channels", label: "Channels", type: "channels" }],
    Render: ({ s, editing }) => <StageCue s={sendAsCue(s)} editing={editing} /> },
  { type: "stage.show", hidden: true, label: "Message display", group: "stage", icon: MessagesSquare,
    description: "Replaced by Cue.", size: { w: 8, h: 8, pw: 4, ph: 8 }, minPx: { w: 200, h: 100 },
    defaults: { channel: "pulpit", style: "big" }, fields: [{ key: "channel", label: "Channel", type: "channel" }],
    Render: ({ s, editing }) => str(s.style, "big") === "conversation" ? <StageShow s={s} editing={editing} /> : <StageCue s={showAsCue(s)} editing={editing} /> },
  { type: "stage.timer", label: "Stage timer", group: "stage", icon: Timer,
    description: "A channel's countdown (started from a Message sender's timer buttons, a macro, or the agent). Same time on every screen.",
    size: { w: 4, h: 2, pw: 4, ph: 2 }, minPx: (s) => (bool(s.controls) ? { w: 260, h: 64 } : { w: 140, h: 64 }), defaults: { channel: "pulpit", controls: false },
    fields: [{ key: "channel", label: "Channel", type: "channel" }, { key: "controls", label: "Pause / +1 / stop buttons", type: "bool", help: "For the booth; leave off on stage screens." }],
    Render: ({ s, editing }) => <StageTimer s={s} editing={editing} /> },
  { type: "stage.live", label: "On air", group: "stage", icon: Radio,
    description: "WE ARE LIVE while the switcher is streaming, OFF AIR when it isn't. Any size — down to a small pill.",
    size: { w: 8, h: 2, pw: 4, ph: 2 }, minPx: { w: 90, h: 28 }, defaults: {},
    fields: [],
    Render: ({ s }) => <StageLive s={s} /> },
  { type: "stage.tally", label: "Tally light", group: "stage", icon: Lightbulb,
    description: "One camera: ON (red) when it's on program, NEXT (green) when it's in preview.",
    size: { w: 3, h: 3, pw: 2, ph: 3 }, minPx: { w: 70, h: 56 }, defaults: { camera: "" },
    fields: [CAMERA_FIELD,
      { key: "input", label: "Switcher input for this camera", type: "input", help: "Only if it differs from Settings → Connections → (switcher) → Cameras on this switcher." }],
    Render: ({ s }) => <TallyLight s={s} /> },

  { type: "video", label: "Video", group: "camera", icon: MonitorPlay,
    description: "Any feed: a camera, the program, slides… Drop several to build a multiview.",
    size: { w: 6, h: 6, ph: 5 }, minPx: { w: 160, h: 100 }, defaults: { feed: "", quality: "auto", tally: false, switchHere: true },
    fields: [
      { key: "feed", label: "Feed", type: "feed", help: "Cameras, plus the feeds under Settings → Video feeds." },
      { key: "switchHere", label: "Let people switch the feed here", type: "bool", help: "A small name tag on the picture; tap it to pick another camera or feed. Two videos swap if needed. Off for stage screens." },
      { key: "quality", label: "Quality", type: "select", showIf: (s) => str(s.feed).startsWith("cam:"), help: "Auto: HD when it's the only video on the screen, SD when there are several or on a phone.",
        options: [{ value: "auto", label: "Auto (recommended)" }, { value: "hd", label: "HD — always" }, { value: "sd", label: "SD — always (lighter)" }] },
      label("title", "Title on the picture"),
      { key: "tally", label: "Tally border (red live, green next)", type: "bool", showIf: (s) => str(s.feed).startsWith("cam:"), help: "From the video switcher: the input whose name matches the camera, or pick it below." },
      { key: "input", label: "Switcher input for tally", type: "input", showIf: (s) => str(s.feed).startsWith("cam:") && bool(s.tally), help: "Only if this one should differ from Settings → Connections → (switcher) → Cameras on this switcher." },
    ],
    Render: ({ s, editing }) => <VideoWidget s={s} editing={editing} /> },
  { type: "camera.live", hidden: true, label: "Live view", group: "camera", icon: MonitorPlay, description: "A camera's picture. Drop several and pick a camera for each to build a multiview.",
    size: { w: 6, h: 6, ph: 5 }, minPx: { w: 160, h: 100 }, defaults: { quality: "auto", camera: "", tally: false },
    fields: [
      CAMERA_FIELD,
      { key: "quality", label: "Quality", type: "select", help: "Auto: HD when it's the only video on the screen, SD when there are several or on a phone.",
        options: [{ value: "auto", label: "Auto (recommended)" }, { value: "hd", label: "HD — always" }, { value: "sd", label: "SD — always (lighter)" }] },
      label("title", "Title on the picture"),
      { key: "tally", label: "Tally border (red live, green next)", type: "bool", help: "From vMix: the input whose name matches the camera, or pick it below." },
      { key: "input", label: "vMix input for tally", type: "input", showIf: (s) => bool(s.tally) },
    ],
    Render: ({ s, editing }) => <LiveCamPinned s={s} editing={editing} /> },
  { type: "camera.move", label: "Move", group: "camera", icon: Gamepad2, description: "Arrows and/or joystick, with Home, zoom, stop and speed.",
    size: { w: 5, h: 10, ph: 10 }, minPx: { w: 260, h: 390 },
    defaults: { controls: "switchable", opensIn: "buttons", home: true, zoom: true, speed: true, startFine: false },
    fields: [
      CAMERA_FIELD,
      { key: "controls", label: "Controls", type: "select", options: [{ value: "switchable", label: "Switchable (arrows ⇄ joystick)" }, { value: "buttons", label: "Arrows only" }, { value: "joystick", label: "Joystick only" }] },
      { key: "opensIn", label: "Opens in", type: "select", options: [{ value: "buttons", label: "Arrows" }, { value: "joystick", label: "Joystick" }], showIf: (s) => str(s.controls, "switchable") === "switchable", help: "Each device then remembers its last choice." },
      { key: "home", label: "Home button", type: "bool", help: "Centre of the arrows / beside the stick." },
      { key: "zoom", label: "Zoom & Stop", type: "bool" },
      { key: "speed", label: "Speed & Fine", type: "bool" },
      { key: "startFine", label: "Start in Fine", type: "bool" },
    ],
    Render: ({ s }) => <MoveWidget s={s} /> },
  { type: "camera.preset", label: "One preset", group: "camera", icon: Bookmark, description: "A single big preset button.",
    size: { w: 3, h: 2, pw: 2, ph: 2 }, minPx: { w: 140, h: 56 }, defaults: {},
    fields: [CAMERA_FIELD, { key: "preset", label: "Preset", type: "source", source: "presets" }, label("label", "Label (optional)")],
    Render: ({ s }) => <OnePreset s={s} /> },
  { type: "camera.presets", label: "Presets", group: "camera", icon: LayoutGrid, description: "All presets.",
    size: { w: 6, h: 6, ph: 7 }, minPx: { w: 280, h: 180 }, defaults: { edit: true }, fields: [CAMERA_FIELD, { key: "edit", label: "Editing", type: "bool", help: "An Edit button to save the current shot into a preset, rename, add and reorder. Off for stage screens." }],
    Render: ({ s }) => <Fill><PresetGrid compact editable={bool(s.edit, true)} /></Fill> },
  { type: "camera.picture", label: "Picture", group: "camera", icon: Sun, description: "Brightness, white balance and the other picture settings.",
    size: { w: 5, h: 9, ph: 10 }, minPx: { w: 260, h: 370 }, defaults: {}, fields: [CAMERA_FIELD],
    Render: () => <Fill><ImageSettings /></Fill> },
  { type: "camera.home", label: "Home", group: "camera", icon: HomeIcon, description: "A single Home button.",
    size: { w: 2, h: 3, pw: 2, ph: 3 }, minPx: { w: 80, h: 60 }, defaults: {}, fields: [CAMERA_FIELD], Render: () => <HomeWidget /> },
  { type: "camera.stop", label: "Stop camera", group: "camera", icon: Square, description: "A big red stop.",
    size: { w: 4, h: 2, pw: 4, ph: 2 }, minPx: { w: 120, h: 60 }, defaults: {}, fields: [CAMERA_FIELD], Render: () => <StopWidget /> },
  { type: "camera.zoom", label: "Zoom", group: "camera", icon: ZoomIn, description: "Tele and wide.",
    size: { w: 2, h: 5, pw: 1, ph: 5 }, minPx: { w: 70, h: 120 }, defaults: {}, fields: [CAMERA_FIELD], Render: () => <ZoomWidget /> },
  { type: "camera.speed", label: "Speed", group: "camera", icon: Gauge, description: "Speed slider and Fine.",
    size: { w: 4, h: 2, pw: 4, ph: 2 }, minPx: { w: 180, h: 110 }, defaults: {}, fields: [CAMERA_FIELD], Render: () => <SpeedWidget /> },

  { type: "switcher.program", hidden: true, label: "Program", group: "switcher", icon: Tv, description: "What the congregation sees.",
    size: { w: 7, h: 8, ph: 5 }, minPx: { w: 160, h: 100 }, defaults: {}, fields: [], Render: ({ s }) => <Feed kind="program" s={s} /> },
  { type: "switcher.confidence", hidden: true, label: "Confidence", group: "switcher", icon: Presentation, description: "Current and next slide.",
    size: { w: 5, h: 6, ph: 5 }, minPx: { w: 160, h: 100 }, defaults: {}, fields: [], Render: ({ s }) => <Feed kind="confidence" s={s} /> },
  { type: "switcher.inputs", label: "Input bank", group: "switcher", icon: LayoutGrid, description: "Tap an input to preview it.",
    size: { w: 6, h: 4, ph: 5 }, minPx: { w: 200, h: 100 }, defaults: {}, fields: [conn("switcher")], Render: () => <Inputs /> },
  { type: "switcher.cutfade", label: "Cut / Fade", group: "switcher", icon: ChevronsRight, description: "Take preview live.",
    size: { w: 4, h: 3, ph: 3 }, minPx: { w: 160, h: 60 }, defaults: { fadeMs: 1000 },
    fields: [conn("switcher"), { key: "fadeMs", label: "Fade length (ms)", type: "number" }], Render: ({ s }) => <CutFade s={s} /> },

  { type: "slides.prevnext", label: "Prev / Next", group: "slides", icon: Presentation, description: "Big slide buttons.",
    size: { w: 4, h: 3, ph: 3 }, minPx: { w: 280, h: 56 }, defaults: {}, fields: [conn("slides")], Render: () => <PrevNext /> },
  { type: "slides.button", label: "Slide button", group: "slides", icon: Square, description: "One slide action (black, logo, clear…).",
    size: { w: 2, h: 2, pw: 2, ph: 2 }, minPx: { w: 90, h: 56 }, defaults: {},
    fields: [conn("slides"), { key: "action", label: "Action", type: "source", source: "slideActions" }, label("label", "Label (optional)")],
    Render: ({ s }) => <SlideButton s={s} /> },

  { type: "audio.favorites", label: "Favorites", group: "audio", icon: ListMusic, description: "All favorite channels.",
    size: { w: 8, h: 6, ph: 9 }, minPx: { w: 280, h: 160 }, defaults: {}, fields: [conn("mixer")], Render: () => <Fill><Mixer /></Fill> },
  { type: "audio.mute", label: "Mute button", group: "audio", icon: Mic, description: "One channel, big.",
    size: { w: 2, h: 4, pw: 2, ph: 3 }, minPx: { w: 100, h: 92 }, defaults: { askUnmute: false, showChannel: true },
    fields: [
      conn("mixer"),
      { key: "slot", label: "Channel", type: "source", source: "favorites" },
      label("label", "Label (optional)"),
      { key: "askUnmute", label: "Ask before unmuting", type: "bool" },
      { key: "showChannel", label: "Show channel number", type: "bool" },
    ],
    Render: ({ s }) => <MuteButton s={s} /> },

  { type: "macros.button", label: "Macro button", group: "macros", icon: Play, description: "Run one macro.",
    size: { w: 2, h: 3, pw: 2, ph: 3 }, minPx: { w: 120, h: 100 }, defaults: {},
    fields: [{ key: "macro", label: "Macro", type: "source", source: "macros" }, label("label", "Label (optional)")],
    Render: ({ s }) => <MacroButton s={s} /> },
  { type: "macros.list", label: "Macro list", group: "macros", icon: ListChecks, description: "All macros, with the builder.",
    size: { w: 5, h: 7, ph: 8 }, minPx: { w: 310, h: 184 }, defaults: {}, fields: [], Render: () => <Fill><Macros /></Fill> },

  { type: "metrics.bandwidth", label: "Video bandwidth", group: "metrics", icon: Activity,
    description: "Live: how much video goes to screens and comes from cameras / vMix, per feed, who's watching, and the vMix SRT link.",
    size: { w: 6, h: 7, pw: 4, ph: 8 }, minPx: { w: 380, h: 210 }, defaults: { viewers: true, senders: true },
    fields: [{ key: "viewers", label: "List who's watching", type: "bool" }, { key: "senders", label: "Show the vMix SRT link", type: "bool" }],
    Render: ({ s }) => <BandwidthWidget s={s} /> },
  { type: "metrics.health", label: "System health", group: "metrics", icon: HeartPulse,
    description: "Cameras, vMix, slides, mixer, stream gateway and stage screens: up or down, since when, and recent problems.",
    size: { w: 4, h: 6, pw: 4, ph: 6 }, minPx: { w: 270, h: 272 }, defaults: { problems: true },
    fields: [{ key: "problems", label: "Show recent problems", type: "bool" }],
    Render: ({ s }) => <HealthWidget s={s} /> },

  { type: "other.clock", label: "Clock", group: "other", icon: Clock, description: "The time, big.",
    size: { w: 3, h: 3, pw: 2, ph: 3 }, minPx: { w: 215, h: 152 }, defaults: { seconds: false, date: false },
    fields: [{ key: "seconds", label: "Show seconds", type: "bool" }, { key: "date", label: "Show the date", type: "bool" }],
    Render: ({ s }) => <ClockWidget s={s} /> },
  { type: "other.countdown", label: "Countdown", group: "other", icon: Timer, description: "Time left until a set time.",
    size: { w: 3, h: 3, pw: 4, ph: 3 }, minPx: { w: 293, h: 105 }, defaults: { target: "11:00" },
    fields: [label("target", "Count down to (HH:MM)"), label("label", "Label (optional)")],
    Render: ({ s }) => <CountdownWidget s={s} /> },
  { type: "other.text", label: "Text / note", group: "other", icon: Type, description: "A note or heading.",
    size: { w: 4, h: 2, pw: 4, ph: 2 }, minPx: { w: 80, h: 40 }, defaults: { size: "md" },
    fields: [label("text", "Text"), { key: "size", label: "Size", type: "select", options: [{ value: "sm", label: "Small" }, { value: "md", label: "Medium" }, { value: "lg", label: "Large" }, { value: "xl", label: "Huge" }] },
      { key: "align", label: "Centre it", type: "select", options: [{ value: "", label: "Left" }, { value: "center", label: "Centre" }] }],
    Render: ({ s }) => <TextWidget s={s} /> },
  { type: "other.schedule", label: "Coming up", group: "macros", icon: Timer, description: "Scheduled actions, with Run now and Cancel.",
    size: { w: 5, h: 4, pw: 4, ph: 4 }, minPx: { w: 260, h: 90 }, defaults: { recent: true },
    fields: [{ key: "recent", label: "Show the last few results", type: "bool" }], Render: ({ s }) => <ComingUp s={s} /> },
  { type: "other.log", label: "Action log", group: "other", icon: ScrollText, description: "Every press, in plain words.",
    size: { w: 6, h: 4, ph: 5 }, minPx: { w: 200, h: 100 }, defaults: {}, fields: [], Render: () => <LogWidget /> },
];

export const WIDGET = Object.fromEntries(WIDGETS.map((w) => [w.type, w])) as Record<string, WidgetDef>;

/** Add-on widgets join the registry as their add-ons load. */
export function registerWidgets(defs: WidgetDef[]) {
  for (const d of defs) WIDGET[d.type] = d;
}

/** Shown for a widget type this build doesn't know (e.g. from a newer version). */
export function UnknownWidget({ type }: { type: string }) {
  return <Unset what={`Unknown widget “${type}”.`} />;
}


