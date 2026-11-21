import { useLayoutEffect, useRef, useState } from "react";
import { Search, X, Copy, Trash2, Loader2, CloudOff } from "lucide-react";
import type { DevicesInfo, Preset, Macro, MixerStatus, PresentationStatus, CameraInfo, SwitcherStatus } from "@/lib/api";
import { useChannels, stageApi } from "@/lib/stage";
import { useLocal, useShared } from "@/lib/hooks";
import { CameraContext, CameraPickContext } from "@/lib/camera";
import { useControl } from "@/lib/control";
import { cn } from "@/lib/cn";
import { Button, Input, Select, Switch } from "@/components/ui";
import { WIDGETS, WIDGET, GROUPS, UnknownWidget, type WidgetDef, type Field, type Settings } from "@/dash/widgets";
import type { Item, Widget } from "@/dash/types";
import { Boundary } from "@/components/Boundary";
import { WidgetConn } from "@/lib/hooks";
import { useAddonWidgets } from "@/dash/addonWidgets";

// Edit-mode pieces of the dashboard page: widget view, library,
// inspector and the save badge.

/** How tall (px) this widget's box must be to show everything in it
 *  without a scrollbar. Lists that grow by themselves (conversation,
 *  action log, viewers…) are marked data-feed and are allowed to scroll. */
function measureNeed(root: HTMLElement): number | null {
  const scrollers = [...root.querySelectorAll<HTMLElement>("*")].filter((el) => {
    if (el.closest("[data-feed]") || el.tagName === "TEXTAREA") return false;
    const oy = getComputedStyle(el).overflowY;
    // A strip that scrolls sideways also computes overflow-y:auto; it isn't
    // cut off below, so it never asks for more height.
    const sideways = el.scrollWidth > el.clientWidth + 1 && el.scrollHeight <= el.clientHeight + 1;
    return (oy === "auto" || oy === "scroll") && !sideways;
  });
  if (!scrollers.length) return null;
  let extra = -Infinity;
  for (const el of scrollers) {
    // Outer heights on both sides: clientHeight leaves out the border, so a
    // bordered box looked 2px short on every pass and the widget grew forever.
    const shown = el.offsetHeight;
    const { height, maxHeight, flex } = el.style;
    // Lay it out at its natural height for a moment (no paint happens).
    el.style.height = "max-content";
    el.style.maxHeight = "none";
    el.style.flex = "none";
    const natural = el.offsetHeight;
    el.style.height = height;
    el.style.maxHeight = maxHeight;
    el.style.flex = flex;
    extra = Math.max(extra, natural - shown);
  }
  return Math.ceil(root.getBoundingClientRect().height + extra);
}

export function WidgetView({ w, editing, onNeed, dash }: { w: Widget; editing: boolean; onNeed?: (id: string, px: number | null) => void; dash?: number }) {
  const def = WIDGET[w.type];
  // The camera a widget drives: this screen's pick (from the widget's
  // camera chip), else the widget's Camera setting.
  const [picked, setPicked] = useLocal<string | null>(`ptz.wcam.${dash ?? 0}.${w.id}`, null);
  const setting = typeof w.settings.camera === "string" ? w.settings.camera : "";
  const cam = picked ?? setting;
  const ref = useRef<HTMLDivElement>(null);
  const last = useRef<number | null | undefined>(undefined);
  useLayoutEffect(() => {
    const root = ref.current;
    if (!root || !onNeed) return;
    let t = 0;
    const run = () => {
      // A short timer, not requestAnimationFrame: rAF stalls in
      // background / throttled tabs and the box would never fit.
      clearTimeout(t);
      t = window.setTimeout(() => {
        const px = measureNeed(root);
        const prev = last.current;
        if (prev === undefined || (px === null) !== (prev === null) || (px !== null && prev !== null && Math.abs(px - prev) > 2)) {
          last.current = px;
          onNeed(w.id, px);
        }
      }, 30);
    };
    run();
    // Re-measure when the box changes size or what's inside changes
    // (a preset added, a channel renamed, inputs loaded…).
    const ro = new ResizeObserver(run);
    ro.observe(root);
    const mo = new MutationObserver(run);
    mo.observe(root, { childList: true, subtree: true, characterData: true });
    return () => {
      clearTimeout(t);
      ro.disconnect();
      mo.disconnect();
    };
  }, [w.id, onNeed]);
  if (!def) return <UnknownWidget type={w.type} />;
  const R = def.Render;
  return (
    <div ref={ref} className={cn("h-full overflow-hidden rounded-[var(--radius-card)]", editing && "pointer-events-none select-none")}>
      <WidgetConn.Provider value={typeof w.settings.connection === "string" ? w.settings.connection : ""}>
        <CameraContext.Provider value={cam}>
          <CameraPickContext.Provider value={editing ? null : (id) => setPicked(id === setting ? null : id)}>
            {def.group === "camera" && def.fields.some((f) => f.type === "camera") && <HoldControl cam={cam} />}
            <Boundary name={def.label}><R s={w.settings} editing={editing} /></Boundary>
          </CameraPickContext.Provider>
        </CameraContext.Provider>
      </WidgetConn.Provider>
    </div>
  );
}

// ---------------------------------------------------------------- library

export function Library({ onAdd, onDragType, onTouchStart, inline, onClose, dragging }: {
  onAdd: (d: WidgetDef) => void; onDragType: (t: string | null) => void; onTouchStart: (d: WidgetDef, e: React.PointerEvent) => void; inline?: boolean;
  onClose?: () => void; dragging?: boolean;
}) {
  const { data: dev } = useShared<DevicesInfo>("/api/devices", 10000);
  const [q, setQ] = useState("");
  const ready = (needs?: string) => {
    if (!needs) return true;
    const k = dev?.[needs as "switcher" | "presentation" | "mixer"];
    return !!k?.configured;
  };
  const addonDefs = useAddonWidgets();
  const match = (w: WidgetDef) => !q || `${w.label} ${w.description} ${w.group}`.toLowerCase().includes(q.toLowerCase());
  return (
    // Big screens: floats over the board (it never takes room from it, so
    // editing shows widgets at their real size). Fades out while dragging
    // from it so the spot under it can take the drop.
    <aside className={inline ? "" : cn("fixed left-4 top-[124px] z-[55] hidden max-h-[calc(100dvh-140px)] w-60 overflow-y-auto rounded-2xl border border-line bg-panel p-3 shadow-[0_12px_40px_rgba(0,0,0,.7)] transition-opacity 2xl:block", dragging && "pointer-events-none opacity-15")}>
      {!inline && (
        <div className="mb-2 flex items-center">
          <span className="text-sm font-semibold">Add widgets</span>
          {onClose && <Button size="icon" variant="ghost" className="ml-auto h-7 w-7" onClick={onClose} aria-label="Close the widget list"><X size={14} /></Button>}
        </div>
      )}
      <div className="relative mb-3">
        <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-dim" />
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search widgets…" className="h-9 pl-8" />
      </div>
      {GROUPS.filter((g) => ready(g.needs)).map((g) => {
        const items = (g.id === "addons" ? addonDefs : WIDGETS).filter((w) => w.group === g.id && !w.hidden && match(w));
        if (!items.length) return null;
        return (
          <div key={g.id} className="mb-3">
            <div className="mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">{g.label}</div>
            <div className="grid grid-cols-2 gap-1.5">
              {items.map((w) => (
                <button key={w.type} type="button" draggable title={w.description}
                  onDragStart={(e) => { e.dataTransfer.setData("text/plain", w.type); e.dataTransfer.effectAllowed = "copy"; onDragType(w.type); }}
                  onDragEnd={() => setTimeout(() => onDragType(null), 0)}
                  onClick={() => onAdd(w)}
                  onPointerDown={(e) => onTouchStart(w, e)}
                  onContextMenu={(e) => e.preventDefault()}
                  className={cn("flex items-center gap-1.5 rounded-lg border border-line bg-panel-2 px-2 text-left text-fg select-none [-webkit-touch-callout:none] hover:border-accent/60 active:scale-[0.98]",
                    inline ? "h-11 text-[13px]" : "h-8 text-[11px]")}>
                  <w.icon size={13} className="shrink-0 text-muted" />
                  <span className="truncate">{w.label}</span>
                </button>
              ))}
            </div>
          </div>
        );
      })}
      {!inline && <p className="text-[11px] leading-snug text-dim">Drag onto the grid, or tap to add at the bottom. Widgets only show for devices that are set up.</p>}
    </aside>
  );
}

// --------------------------------------------------------------- inspector

export function Inspector({ w, onChange, onRemove, onDuplicate, onClose, pos, side = "right" }: {
  w: Widget; onChange: (s: Settings) => void; onRemove: () => void; onDuplicate: () => void; onClose: () => void; pos?: Item;
  /** Which side it floats on (tablets and up): away from the widget. */
  side?: "left" | "right";
}) {
  const def = WIDGET[w.type];
  const s = w.settings;
  return (
    // Phones: a sheet over the bottom. Bigger: a panel floating over the
    // board on the side away from the widget. Never beside the board, so
    // the board keeps the size it has after Done.
    <aside className={cn("fixed inset-x-0 bottom-0 z-[60] max-h-[60dvh] overflow-y-auto rounded-t-2xl border border-line bg-panel p-4 shadow-[0_12px_40px_rgba(0,0,0,.7)] md:inset-x-auto md:bottom-auto md:top-[124px] md:w-72 md:max-h-[calc(100dvh-140px)] md:rounded-2xl short:md:top-24 short:md:max-h-[calc(100dvh-104px)]",
      side === "left" ? "md:left-4" : "md:right-4")}>
      <div className="mb-1 flex items-center gap-2">
        <span className="text-sm font-semibold">{def?.label ?? w.type}</span>
        <Button size="icon" variant="ghost" className="ml-auto h-8 w-8" onClick={onClose} aria-label="Close"><X size={15} /></Button>
      </div>
      <div className="mb-3 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">{def ? GROUPS.find((g) => g.id === def.group)?.label : "unknown"}</div>
      {def?.description && <p className="mb-3 text-xs text-muted">{def.description}</p>}
      {def?.group === "camera" && !def.fields.some((f) => f.type === "camera" || f.type === "feed") && (
        <p className="mb-3 rounded-lg border border-line bg-bg px-2.5 py-2 text-[11px] text-muted">Drives the camera picked at the top of the screen.</p>
      )}
      <div className="space-y-3">
        {def?.fields.filter((f) => !f.showIf || f.showIf(s)).map((f) => (
          <FieldInput key={f.key} f={f} all={s} value={s[f.key] ?? def.defaults[f.key]} onChange={(v) => onChange({ ...s, [f.key]: v })} />
        ))}
        {def && def.fields.length === 0 && <p className="text-xs text-dim">Nothing to set — drag and resize it on the grid.</p>}
      </div>
      {pos && <p className="mt-4 font-mono text-[11px] text-dim">col {pos.x + 1} · row {pos.y + 1} · {pos.w} × {pos.h}</p>}
      <div className="mt-4 grid grid-cols-2 gap-2">
        <Button size="sm" onClick={onDuplicate}><Copy size={13} /> Duplicate</Button>
        <Button size="sm" variant="danger" onClick={onRemove}><Trash2 size={13} /> Remove</Button>
      </div>
    </aside>
  );
}

function FieldInput({ f, value, onChange, all }: { f: Field; value: unknown; onChange: (v: unknown) => void; all: Settings }) {
  const opts = useSourceOptions(f, typeof all.camera === "string" ? all.camera : "");
  const label = <span className="mb-1 block text-xs text-muted">{f.label}</span>;
  const help = f.help && <span className="mt-1 block text-[11px] text-dim">{f.help}</span>;
  if (f.type === "bool") {
    return (
      <label className="flex items-center justify-between gap-3 text-sm">
        <span>{f.label}{help}</span>
        <Switch checked={!!value} onChange={onChange} label={f.label} />
      </label>
    );
  }
  if (f.type === "list") {
    const lines = Array.isArray(value) ? (value as string[]) : [];
    return <ListField label={f.label} help={f.help} value={lines} onChange={onChange} />;
  }
  if (f.type === "channel" || f.type === "channels") {
    return <ChannelField f={f} value={value} onChange={onChange} />;
  }
  if (f.type === "feed") {
    return <FeedField f={f} value={typeof value === "string" ? value : ""} onChange={onChange} />;
  }
  if (f.type === "connection") {
    return <ConnectionField f={f} value={typeof value === "string" ? value : ""} onChange={onChange} />;
  }
  if (f.type === "camera") {
    return <CameraField f={f} value={typeof value === "string" ? value : ""} onChange={onChange} />;
  }
  if (f.type === "input") {
    return <InputField f={f} value={Number(value) || 0} onChange={onChange} />;
  }
  if (f.type === "select" || f.type === "source") {
    const options = f.type === "select" ? f.options ?? [] : [{ value: "", label: "Choose…" }, ...opts];
    return <label className="block">{label}<Select value={value === undefined || value === null ? "" : String(value)} onChange={(v) => onChange(f.source === "favorites" ? Number(v) : v)} options={options} />{help}</label>;
  }
  return (
    <label className="block">{label}
      <Input type={f.type === "number" ? "number" : "text"} value={value === undefined || value === null ? "" : String(value)}
        onChange={(e) => onChange(f.type === "number" ? Number(e.target.value) : e.target.value)} />{help}
    </label>
  );
}

function useSourceOptions(f: Field, cam: string): { value: string; label: string }[] {
  const presets = useShared<Preset[]>(f.source === "presets" ? (cam ? `/api/presets?cam=${encodeURIComponent(cam)}` : "/api/presets") : null, 0).data;
  const macros = useShared<Macro[]>(f.source === "macros" ? "/api/macros" : null, 0).data;
  const mixer = useShared<MixerStatus>(f.source === "favorites" ? "/api/mixer/status" : null, 0).data;
  const slides = useShared<PresentationStatus>(f.source === "slideActions" ? "/api/presentation/status" : null, 0).data;
  const ref = useRef<{ value: string; label: string }[]>([]);
  switch (f.source) {
    case "presets": ref.current = (presets ?? []).map((p) => ({ value: p.name, label: p.name })); break;
    case "macros": ref.current = (macros ?? []).map((m) => ({ value: m.name, label: m.name })); break;
    case "favorites": ref.current = (mixer?.channels ?? []).map((c) => ({ value: String(c.slot), label: `${c.label}${c.channel ? ` (Ch ${c.channel})` : " (unassigned)"}` })); break;
    case "slideActions": ref.current = (slides?.actions ?? []).map((a) => ({ value: a, label: a })); break;
  }
  return ref.current;
}

// ------------------------------------------------------ new field kinds

/** One item per line; reorder with the arrows. */
function ListField({ label, help, value, onChange }: { label: string; help?: string; value: string[]; onChange: (v: string[]) => void }) {
  const [draft, setDraft] = useState("");
  const set = (next: string[]) => onChange(next.map((x) => x.trim()).filter(Boolean));
  const move = (i: number, d: number) => {
    const n = [...value];
    const j = i + d;
    if (j < 0 || j >= n.length) return;
    [n[i], n[j]] = [n[j], n[i]];
    set(n);
  };
  return (
    <div>
      <span className="mb-1 block text-xs text-muted">{label}</span>
      <div className="space-y-1 rounded-lg border border-line bg-bg p-1.5">
        {value.map((v, i) => (
          <div key={i} className="flex items-center gap-1">
            <Input value={v} className="h-8 flex-1 text-sm" onChange={(e) => { const n = [...value]; n[i] = e.target.value; onChange(n); }}
              onBlur={() => set(value)} aria-label={`${label} ${i + 1}`} />
            <button type="button" className="h-8 w-6 text-xs text-muted disabled:opacity-30" disabled={i === 0} onClick={() => move(i, -1)} aria-label="Move up">▲</button>
            <button type="button" className="h-8 w-6 text-xs text-muted disabled:opacity-30" disabled={i === value.length - 1} onClick={() => move(i, 1)} aria-label="Move down">▼</button>
            <button type="button" className="h-8 w-6 text-muted hover:text-bad" onClick={() => set(value.filter((_, k) => k !== i))} aria-label="Remove"><X size={13} /></button>
          </div>
        ))}
        <form className="flex gap-1" onSubmit={(e) => { e.preventDefault(); if (draft.trim()) { set([...value, draft]); setDraft(""); } }}>
          <Input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="＋ Add…" className="h-8 flex-1 text-sm" />
          <Button size="sm" type="submit" disabled={!draft.trim()}>Add</Button>
        </form>
      </div>
      {help && <span className="mt-1 block text-[11px] text-dim">{help}</span>}
    </div>
  );
}

const NEW_COLORS = ["#F5A524", "#60A5FA", "#4ADE80", "#F472B6", "#A78BFA", "#F87171"];

function ChannelField({ f, value, onChange }: { f: Field; value: unknown; onChange: (v: unknown) => void }) {
  const { channels } = useChannels();
  const multi = f.type === "channels";
  const picked = multi ? (Array.isArray(value) ? (value as string[]) : []) : [typeof value === "string" ? value : ""];
  const [err, setErr] = useState("");
  const create = async () => {
    const name = window.prompt("New channel name (e.g. Kids room)")?.trim();
    if (!name) return;
    try {
      const c = await stageApi.create(name, NEW_COLORS[channels.length % NEW_COLORS.length]);
      onChange(multi ? [...picked, c.id] : c.id);
      setErr("");
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  const missing = picked.filter((id) => id && !channels.some((c) => c.id === id));
  return (
    <div>
      <span className="mb-1 block text-xs text-muted">{f.label}</span>
      <div className="space-y-0.5 rounded-lg border border-line bg-bg p-1">
        {channels.map((c) => {
          const on = picked.includes(c.id);
          const others = c.screens.length;
          return (
            <button key={c.id} type="button"
              onClick={() => onChange(multi ? (on ? picked.filter((x) => x !== c.id) : [...picked, c.id]) : c.id)}
              className={cn("flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm", on ? "bg-raise" : "hover:bg-panel-2")}>
              <span className="w-4 text-accent">{on ? "✓" : ""}</span>
              <span className="h-2.5 w-2.5 rounded-full" style={{ background: c.color }} />
              <span className="flex-1 truncate">{c.name}</span>
              <span className="text-[11px] text-dim">{others} online</span>
            </button>
          );
        })}
        <button type="button" onClick={create} className="w-full rounded-md px-2 py-1.5 text-left text-sm text-muted hover:bg-panel-2">＋ New channel…</button>
      </div>
      {missing.length > 0 && <span className="mt-1 block text-[11px] text-bad">Linked to a deleted channel — pick another.</span>}
      {err && <span className="mt-1 block text-[11px] text-bad">{err}</span>}
      <span className="mt-1 block text-[11px] text-dim">Widgets on the same channel are linked, on any screen. Manage channels in Settings → Stage channels.</span>
    </div>
  );
}

/** One-tap choices (up to 8), else a dropdown. */
function Choice({ label, help, value, options, onChange, add }: {
  label: string; help?: string; value: string; options: { value: string; label: string }[]; onChange: (v: string) => void; add?: string;
}) {
  return (
    <div>
      <span className="mb-1.5 block text-xs font-medium text-muted">{label}</span>
      {options.length <= 8 ? (
        <div className="flex flex-wrap gap-1.5">
          {options.map((o) => (
            <button key={o.value} type="button" onClick={() => onChange(o.value)}
              className={cn("rounded-lg border px-2.5 py-1.5 text-xs font-medium transition",
                o.value === value ? "border-accent bg-accent text-bg" : "border-line bg-bg text-muted hover:text-fg")}>
              {o.label}
            </button>
          ))}
        </div>
      ) : <Select value={value} onChange={onChange} options={options} />}
      {add && <a href="/settings" className="mt-1.5 inline-block text-[11px] font-medium text-accent hover:underline">+ {add}</a>}
      {help && <span className="mt-1 block text-[11px] text-dim">{help}</span>}
    </div>
  );
}

function CameraField({ f, value, onChange }: { f: Field; value: string; onChange: (v: string) => void }) {
  const cams = useShared<CameraInfo[]>("/api/cameras", 10000).data ?? [];
  const options = cams.map((c) => ({ value: c.main ? "" : c.id, label: c.name + (c.main ? " (main)" : "") }));
  return <Choice label={f.label} help={f.help} value={value} options={options} onChange={onChange} add="Add a camera (Settings → Cameras)" />;
}

function InputField({ f, value, onChange }: { f: Field; value: number; onChange: (v: number) => void }) {
  const sw = useShared<SwitcherStatus>("/api/switcher/status", 0).data;
  const options = [{ value: "0", label: "Auto (match the camera's name)" }, ...(sw?.inputs ?? []).map((i) => ({ value: String(i.number), label: `${i.number} · ${i.title}` }))];
  return (
    <label className="block"><span className="mb-1 block text-xs text-muted">{f.label}</span>
      <Select value={String(value)} onChange={(v) => onChange(Number(v))} options={options} />
      {f.help && <span className="mt-1 block text-[11px] text-dim">{f.help}</span>}
    </label>
  );
}

export function SaveBadge({ state, onRetry }: { state: "saved" | "pending" | "saving" | "error"; onRetry: () => void }) {
  if (state === "error") {
    return (
      <button type="button" onClick={onRetry} className="flex items-center gap-1 rounded-lg border border-pgm/40 bg-pgm/15 px-2 py-1 text-[11px] font-semibold text-bad">
        <CloudOff size={12} /> Not saved · Retry
      </button>
    );
  }
  if (state === "saving" || state === "pending") {
    return <span className="flex items-center gap-1 text-[11px] text-muted"><Loader2 size={12} className="animate-spin" /> Saving…</span>;
  }
  return <span className="flex items-center gap-1 text-[11px] text-good"><span className="h-1.5 w-1.5 rounded-full bg-good" /> Saved</span>;
}

/** Which connection a widget uses: only shown when there's a choice. */
function ConnectionField({ f, value, onChange }: { f: Field; value: string; onChange: (v: string) => void }) {
  const { data } = useShared<{ connections: { id: string; name: string; addon: string; capabilities: string[]; ready: boolean }[] }>("/api/devices", 10000);
  const list = (data?.connections ?? []).filter((c) => (f.cap ? c.capabilities?.includes(f.cap) : true) && (f.addon ? c.addon === f.addon : true));
  if (list.length <= 1 && !value) return null;
  const options = [{ value: "", label: `The first one${list[0] ? ` (${list[0].name})` : ""}` }, ...list.map((c) => ({ value: c.id, label: c.name + (c.ready ? "" : " — not connected") }))];
  if (value && !list.some((c) => c.id === value)) options.push({ value, label: "(removed connection)" });
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-muted">{f.label}</span>
      <Select value={value} onChange={onChange} options={options} />
      {f.help && <span className="mt-1 block text-[11px] text-dim">{f.help}</span>}
    </label>
  );
}

/** Which video feed a Video widget shows: cameras plus Settings → Video feeds. */
function FeedField({ f, value, onChange }: { f: Field; value: string; onChange: (v: string) => void }) {
  const { data } = useShared<{ feeds: { id: string; name: string; source: string }[] }>("/api/feeds", 10000);
  const feeds = data?.feeds ?? [];
  const options = feeds.map((x) => ({ value: x.id, label: x.name }));
  if (value && value !== "cam:" && !feeds.some((x) => x.id === value)) options.push({ value, label: "(removed feed)" });
  return <Choice label={f.label} help={f.help} value={value} options={options} onChange={onChange} add="Add a camera or feed (Settings)" />;
}

/** Keeps a camera widget's control link open while it's on screen. */
function HoldControl({ cam }: { cam: string }) {
  useControl(cam);
  return null;
}
