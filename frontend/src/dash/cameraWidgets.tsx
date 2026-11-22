// PTZOptics camera controls, per camera (design/camera-controls-mock.png):
// speeds with Motion Sync, focus with lock, picture scenes and the
// camera's on-screen menu. Each widget picks its camera (the Camera
// setting); every value is kept with that camera.
import { useEffect, useState } from "react";
import { ChevronUp, ChevronDown, ChevronLeft, ChevronRight, Lock, LockOpen, Moon, Sun, CloudMoon, Plus, Trash2 } from "lucide-react";
import { api } from "@/lib/api";
import { useShared } from "@/lib/hooks";
import { useCam, withCam } from "@/lib/camera";
import { useLog } from "@/lib/log";
import { capture } from "@/lib/capture";
import { cn } from "@/lib/cn";
import { useControls, type Controls } from "@/lib/camctl";
import { Button, Dialog, Input, Slider, Switch } from "@/components/ui";
import { Frame, Ctx, CamSource } from "./header";
import type { CameraInfo } from "@/lib/api";

// ---------------------------------------------------------------- speeds

const SPEEDS: { key: keyof Controls & `${string}Speed`; label: string; min: number; max: number; zero?: string }[] = [
  { key: "panSpeed", label: "Pan", min: 1, max: 24 },
  { key: "tiltSpeed", label: "Tilt", min: 1, max: 20 },
  { key: "zoomSpeed", label: "Zoom", min: 1, max: 7 },
  { key: "focusSpeed", label: "Focus", min: 0, max: 8, zero: "Std" },
  { key: "recallSpeed", label: "Preset recall", min: 0, max: 24, zero: "Cam" },
];

export function SpeedsWidget() {
  const { ctl, call } = useControls();
  const { log } = useLog();
  // While a slider moves, show its value here; the camera gets it on release.
  const [drag, setDrag] = useState<Partial<Record<string, number>>>({});
  const save = async (body: Record<string, unknown>, what: string) => {
    try { await call("PATCH", "/api/camera/controls", body); log("Camera", what); }
    catch (e) { log("Camera", `${what} failed — ${(e as Error).message}`, false); }
  };
  return (
    <Frame title="Speed" source={<CamSource />}>
      <div className="flex h-full flex-col justify-between gap-1 overflow-hidden">
        {SPEEDS.map((sp) => {
          const v = drag[sp.key] ?? (ctl[sp.key] as number);
          return (
            <div key={sp.key} className="flex min-h-7 items-center gap-3">
              <span className="w-[clamp(44px,28%,104px)] shrink-0 truncate text-[13px] text-muted">{sp.label}</span>
              <Slider value={v} min={sp.min} max={sp.max} label={`${sp.label} speed`} className="min-w-0 flex-1"
                onChange={(n) => setDrag((d) => ({ ...d, [sp.key]: n }))}
                onCommit={(n) => { save({ [sp.key]: n }, `${sp.label} speed ${n}`).finally(() => setDrag((d) => ({ ...d, [sp.key]: undefined }))); }} />
              <span className="w-8 shrink-0 text-right font-mono text-[13px] font-bold tabular text-fg" title={v === 0 && sp.zero ? (sp.key === "focusSpeed" ? "The camera's standard focus speed" : "The camera's own preset speed") : undefined}>{v === 0 && sp.zero ? sp.zero : v}</span>
            </div>
          );
        })}
        <label className="recess mt-1 flex min-h-10 cursor-pointer items-center gap-3 rounded-xl border px-3 py-1.5">
          <span className="min-w-0 flex-1 truncate text-[13px] text-fg">Motion Sync <span className="text-dim @max-[300px]:hidden">(pan + tilt + zoom arrive together)</span></span>
          <Switch checked={!!ctl.motionSync} onChange={(on) => save({ motionSync: on }, `Motion Sync ${on ? "on" : "off"}`)} label="Motion Sync" />
        </label>
      </div>
    </Frame>
  );
}

// ----------------------------------------------------------------- focus

function useCamInfo() {
  const cam = useCam();
  const { data } = useShared<CameraInfo[]>("/api/cameras", 3000);
  return data?.find((c) => c.id === cam) ?? data?.find((c) => c.main);
}

export function FocusWidget() {
  const { ctl, call, cam } = useControls();
  const info = useCamInfo();
  const { log } = useLog();
  const auto = info?.status?.focusAuto ?? null;
  const locked = ctl.focusLocked;
  const focus = withCam("/api/focus", cam);
  const act = async (label: string, action: string) => {
    try { await api(focus, "POST", { action }); log("Camera", label); }
    catch (e) { log("Camera", `${label} failed — ${(e as Error).message}`, false); }
  };
  const lock = async () => {
    try { await call("POST", "/api/camera/focus-lock", { locked: !locked }); log("Camera", locked ? "Focus unlocked" : "Focus locked"); }
    catch (e) { log("Camera", `Focus lock failed — ${(e as Error).message}`, false); }
  };
  const [held, setHeld] = useState<"" | "near" | "far">("");
  const hold = (dir: "near" | "far") => ({
    onPointerDown: (e: React.PointerEvent) => { if (auto || locked) return; e.preventDefault(); capture(e); setHeld(dir); api(focus, "POST", { action: dir }).catch(() => {}); },
    onPointerUp: () => { if (held) { setHeld(""); api(focus, "POST", { action: "stop" }).catch(() => {}); } },
    onPointerCancel: () => { if (held) { setHeld(""); api(focus, "POST", { action: "stop" }).catch(() => {}); } },
    onLostPointerCapture: () => { if (held) { setHeld(""); api(focus, "POST", { action: "stop" }).catch(() => {}); } },
    onContextMenu: (e: React.MouseEvent) => e.preventDefault(),
  });
  const mode = auto === null ? "" : auto ? "Auto" : "Manual";
  const off = !!auto || locked;
  const seg = (active: boolean) => cn("h-full min-w-0 flex-1 truncate rounded-[10px] px-2 text-[13px] font-semibold transition", active ? "btn-sel border" : "text-muted hover:text-fg");
  return (
    <Frame title="Focus" source={<CamSource />} right={mode && <Ctx><span className={locked ? "text-accent" : undefined}>{mode}{locked ? " · locked" : ""}</span></Ctx>}>
      <div className="flex h-full flex-col gap-2">
        <div className="recess flex h-10 shrink-0 gap-0.5 rounded-xl border p-0.5" role="tablist">
          <button type="button" role="tab" aria-selected={auto === true} disabled={locked} className={seg(auto === true)} onClick={() => act("Auto focus", "auto")}>Auto</button>
          <button type="button" role="tab" aria-selected={auto === false} disabled={locked} className={seg(auto === false)} onClick={() => act("Manual focus", "manual")}>Manual</button>
          <button type="button" disabled={locked} className={seg(false)} onClick={() => act("One-push focus", "onepush")}>One-push</button>
        </div>
        <div className="grid min-h-0 flex-1 grid-cols-2 gap-2">
          {(["near", "far"] as const).map((d) => (
            <button key={d} type="button" aria-label={d === "near" ? "Focus near" : "Focus far"} disabled={off} {...hold(d)}
              className={cn("touch-hold flex min-h-7 items-center justify-center gap-2 rounded-2xl border text-lg font-bold transition",
                off ? "btn-off" : held === d ? "btn-sel" : "btn-raised hover:brightness-125 active:scale-[0.98]")}>
              {d === "near" ? <>− Near</> : <>Far +</>}
            </button>
          ))}
        </div>
        <button type="button" onClick={lock}
          className={cn("flex min-h-8 shrink-0 items-center justify-center gap-2 rounded-xl border px-3 py-1.5 text-[13px] font-bold transition active:scale-[0.99]",
            locked ? "btn-sel" : "btn-raised text-muted hover:brightness-125")}>
          {locked ? <Lock size={15} /> : <LockOpen size={15} />}
          <span className="truncate">{locked ? "Focus locked — tap to unlock" : "Lock focus"}</span>
        </button>
      </div>
    </Frame>
  );
}

// ---------------------------------------------------------------- scenes

const SCENE_ICON: Record<string, React.ReactNode> = {
  "builtin:lowlight": <Moon size={15} />, "builtin:sportsday": <Sun size={15} />, "builtin:sportsnight": <CloudMoon size={15} />,
};

export function ScenesWidget({ builtins = true }: { builtins?: boolean }) {
  const { ctl, call } = useControls();
  const { log } = useLog();
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const all = [...(builtins ? ctl.builtinScenes : []), ...ctl.scenes];
  const apply = async (id: string, n: string) => {
    try { await call("POST", `/api/camera/scenes/${encodeURIComponent(id)}/apply`); log("Camera", `Scene “${n}”`); }
    catch (e) { log("Camera", `Scene “${n}” failed — ${(e as Error).message}`, false); }
  };
  const save = async () => {
    const n = name.trim();
    if (!n) return;
    try { await call("POST", "/api/camera/scenes", { name: n }); log("Camera", `Saved the picture as “${n}”`); setSaving(false); setName(""); }
    catch (e) { log("Camera", `Save scene failed — ${(e as Error).message}`, false); }
  };
  const del = async (id: string, n: string) => {
    try { await call("DELETE", `/api/camera/scenes/${encodeURIComponent(id)}`); log("Camera", `Deleted scene “${n}”`); }
    catch (e) { log("Camera", (e as Error).message, false); }
  };
  const cols = all.length <= 2 ? 1 : 2;
  return (
    <Frame title="Picture scenes" source={<CamSource />}
      right={<button type="button" onClick={() => setSaving(true)} className="flex min-h-7 min-w-7 items-center justify-center gap-1 text-[12px] font-semibold text-accent hover:brightness-125"><Plus size={13} /><span className="@max-[260px]:hidden">Save current</span></button>}>
      {all.length === 0 ? (
        <div className="flex h-full items-center justify-center text-center text-xs text-dim">No scenes yet. Set the picture, then “Save current”.</div>
      ) : (
        <div className="grid h-full gap-2" style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`, gridAutoRows: "minmax(28px, 1fr)" }}>
          {all.map((sc) => {
            const active = ctl.activeScene === sc.id, builtin = sc.id.startsWith("builtin:");
            return (
              <button key={sc.id} type="button" onClick={() => apply(sc.id, sc.name)} aria-pressed={active}
                className={cn("flex min-w-0 flex-col items-center justify-center gap-0.5 overflow-hidden rounded-2xl border px-2 transition active:scale-[0.98]",
                  active ? "btn-sel" : "btn-raised hover:brightness-125")}>
                <span className="flex max-w-full items-center gap-1.5 truncate text-[15px] font-bold">{SCENE_ICON[sc.id]}<span className="truncate">{sc.name}</span></span>
                <span className={cn("smallcaps truncate @max-h-[120px]:hidden", active && "text-accent")}>{builtin ? "built-in" : "yours"}{active ? " · active" : ""}</span>
              </button>
            );
          })}
        </div>
      )}
      <Dialog open={saving} onOpenChange={setSaving} title="Save the current picture" description="Exposure, white balance, colour and noise reduction as the camera has them now. Tap the scene later to put them back.">
        <form onSubmit={(e) => { e.preventDefault(); save(); }} className="flex gap-2">
          <Input autoFocus value={name} maxLength={40} onChange={(e) => setName(e.target.value)} placeholder="Scene name, e.g. Sermon" />
          <Button type="submit" variant="accent">Save</Button>
        </form>
        {ctl.scenes.length > 0 && (
          <div className="mt-4 flex flex-col gap-1.5">
            <span className="smallcaps">Your scenes</span>
            {ctl.scenes.map((sc) => (
              <div key={sc.id} className="recess flex items-center gap-2 rounded-xl border px-3 py-1.5 text-sm">
                <span className="flex-1 truncate">{sc.name}</span>
                <Button size="sm" variant="ghost" aria-label={`Delete ${sc.name}`} onClick={() => del(sc.id, sc.name)}><Trash2 size={14} /></Button>
              </div>
            ))}
          </div>
        )}
      </Dialog>
    </Frame>
  );
}

// ------------------------------------------------------- on-screen menu

export function MenuWidget() {
  const cam = useCam();
  const { log } = useLog();
  const [open, setOpen] = useState(false);
  useEffect(() => setOpen(false), [cam]);
  const send = async (action: string, label: string) => {
    try {
      await api(withCam("/api/camera/osd", cam), "POST", { action });
      if (action === "open") setOpen((o) => !o);
      if (action === "close") setOpen(false);
      log("Camera", `Menu: ${label}`);
    } catch (e) { log("Camera", `Menu ${label} failed — ${(e as Error).message}`, false); }
  };
  const arrow = (action: string, label: string, Icon: typeof ChevronUp, pos: string) => (
    <button type="button" aria-label={label} onClick={() => send(action, label)}
      className={cn("absolute flex h-[24%] w-[24%] min-h-7 min-w-7 items-center justify-center rounded-full text-fg/80 transition hover:text-fg active:scale-90", pos)}>
      <Icon className="h-[60%] w-[60%]" strokeWidth={2.5} />
    </button>
  );
  return (
    <Frame title="Camera menu" source={<CamSource />} right={open && <Ctx><span className="text-accent">on-screen menu OPEN</span></Ctx>}>
      <div className="flex h-full items-center justify-center gap-3 [container-type:size]">
        <div className="btn-raised relative aspect-square w-[min(240px,100cqh,100cqw-112px)] shrink-0 rounded-full border">
          {arrow("up", "Up", ChevronUp, "left-1/2 top-[3%] -translate-x-1/2")}
          {arrow("down", "Down", ChevronDown, "bottom-[3%] left-1/2 -translate-x-1/2")}
          {arrow("left", "Left", ChevronLeft, "left-[3%] top-1/2 -translate-y-1/2")}
          {arrow("right", "Right", ChevronRight, "right-[3%] top-1/2 -translate-y-1/2")}
          <button type="button" onClick={() => send("enter", "Enter")}
            className="btn-sel absolute left-1/2 top-1/2 flex aspect-square w-[38%] min-w-7 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border text-[clamp(11px,6cqh,16px)] font-bold transition active:scale-95">
            Enter
          </button>
        </div>
        <div className="flex h-[min(240px,100cqh)] w-24 shrink-0 flex-col gap-2">
          {([["open", "Menu"], ["back", "Back"], ["close", "Close menu"]] as const).map(([a, l]) => (
            <button key={a} type="button" onClick={() => send(a, l)}
              className="btn-raised flex min-h-7 flex-1 items-center justify-center rounded-xl border px-1 text-[13px] font-bold transition hover:brightness-125 active:scale-[0.97]">
              <span className="truncate">{l}</span>
            </button>
          ))}
        </div>
      </div>
    </Frame>
  );
}
