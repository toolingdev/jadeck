import { Puzzle } from "lucide-react";
import { useShared } from "@/lib/hooks";
import { cn } from "@/lib/cn";
import { useMemo } from "react";
import { WIDGET_KINDS, fieldSettings, resolveBind, type Manifest, type WidgetDef as AddonWidgetDef, type WidgetKind } from "@/lib/addons";
import { registerWidgets, type Settings, type WidgetDef } from "./widgets";
import { AddonSample, bound, str, useAddonAction, useAddonState, type ShapeProps } from "./addon/common";
import { Chat } from "./addon/chat";
import { ImageShape } from "./addon/image";
import { ButtonsShape } from "./addon/buttons";
import { InputShape } from "./addon/input";
import { StripShape } from "./addon/strip";
import { SectionsShape } from "./addon/sections";
import { TextShape } from "./addon/text";
import { ClockShape } from "./addon/clock";
import { SliderShape } from "./addon/slider";
import { FrameShape } from "./addon/frame";

// Widgets an add-on brings (design/WIDGETS-V2.md), drawn by the controller
// from the add-on's state in one of the shared shapes, or by the add-on
// itself in a sandboxed frame. Their dashboard type is
// addon:<add-on id>:<widget>; settings.connection picks the connection
// (defaults to the add-on's first one). Kind "core" reuses a built-in.

export { AddonSample };

const SHAPES: Partial<Record<WidgetKind, (p: ShapeProps) => React.ReactNode>> = {
  status: StatusShape, list: ListShape, image: ImageShape, buttons: ButtonsShape, input: InputShape, strip: StripShape,
  sections: SectionsShape, text: TextShape, clock: ClockShape, slider: SliderShape, frame: FrameShape,
};

export function AddonWidget({ def: raw, s, manifest, editing = false, widgetId, dash }: {
  def: AddonWidgetDef; s: Settings; manifest?: Manifest; editing?: boolean;
  /** The dashboard widget (v2.1): with add-on fields, state requests name it so the add-on gets its settings. */
  widgetId?: string; dash?: number;
}) {
  const addonId = str(s.addon) || manifest?.id || "";
  // v2.1: the widget's add-on settings (field defaults, then saved values)
  // and its binds with "$settings.<key>" filled in.
  const settings = useMemo(() => fieldSettings(raw.fields, s), [raw.fields, s]);
  const settingsKey = JSON.stringify(settings);
  const def = useMemo(() => (raw.fields?.length ? { ...raw, bind: resolveBind(raw.bind, settings) } : raw),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [raw, settingsKey]);
  const widget = raw.fields?.length && widgetId ? { id: widgetId, dash, settingsKey } : undefined;
  const { state, conn, none, preview } = useAddonState(addonId, str(s.connection), widget);
  const shell = (body: React.ReactNode) => (
    <div className="card-surface flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line">
      <div className="shrink-0 truncate px-3 pt-2.5 pb-1.5 text-[13px] font-bold leading-[18px]">{def.label}</div>
      <div className="min-h-0 flex-1 px-3 pb-3">{body}</div>
    </div>
  );
  const Shape = def.kind === "chat" ? null : SHAPES[def.kind];
  if (def.kind !== "chat" && !Shape) return shell(<p className="text-xs text-dim">Needs a newer jadeck (widget kind “{String(def.kind)}”).</p>);
  if (none) return shell(<p className="text-xs text-dim">Add a connection for this add-on (Settings → Connections).</p>);
  if (!state) return shell(<p className="text-xs text-dim">Loading…</p>);
  if (state.error) return shell(<p className="line-clamp-3 text-xs text-bad">{String(state.error)}</p>);
  if (def.kind === "chat") return <Chat label={def.label} state={state} conn={conn} />;
  const S = Shape!;
  return <S def={def} state={state} conn={conn} s={s} editing={editing} preview={preview} manifest={manifest} />;
}

// ------------------------------------------------------------- v1 shapes

function StatusShape({ def, state }: ShapeProps) {
  const b = def.bind ?? {};
  const on = !!bound(state, b, "on", "on");
  const title = str(bound(state, b, "title", "title"));
  const slide = bound(state, b, "slide") as number | undefined, slides = bound(state, b, "slides") as number | undefined;
  return (
    <div className={cn("card-surface flex h-full flex-col justify-center gap-1 rounded-[var(--radius-card)] border-2 px-4 transition-[border-color,box-shadow]",
      on ? "border-pgm shadow-[inset_0_0_18px_color-mix(in_srgb,var(--color-pgm)_18%,transparent)]" : "border-line")}>
      <span className={cn("flex items-center gap-2 text-sm font-bold tracking-[0.12em]", on ? "text-bad" : "text-dim")}>
        {/* 12px dot; the pulse is for on air only (WIDGETS.md §6) */}
        <span className={cn("h-3 w-3 shrink-0 rounded-full", on ? "animate-pulse bg-pgm shadow-[0_0_8px_var(--color-pgm)]" : "bg-unlit border border-line-strong")} />{on ? "ON AIR" : "OFF AIR"}
      </span>
      {on && title && <span className="truncate text-base font-semibold">{title}{slide ? ` · slide ${slide}${slides ? ` of ${slides}` : ""}` : ""}</span>}
    </div>
  );
}

function ListShape({ def, state, conn, preview, editing }: ShapeProps) {
  const b = def.bind ?? {};
  const items = (bound(state, b, "items", "items") as { index: number; title: string }[] | undefined) ?? [];
  const cur = Number(bound(state, b, "current", "current")) || 0;
  const action = str(b.action);
  const { run, busy } = useAddonAction(conn, preview);
  return (
    <div className="card-surface flex h-full flex-col overflow-hidden rounded-[var(--radius-card)] border border-line">
      <div className="shrink-0 truncate px-3 pt-2.5 pb-1.5 text-[13px] font-bold leading-[18px]">{def.label}</div>
      <div className="min-h-0 flex-1 px-3 pb-3">
        {items.length === 0 ? <p className="text-xs text-dim">{state.onAir === false ? "Not on air." : "Nothing in the list."}</p> : (
          <div className="h-full space-y-1 overflow-y-auto" data-feed>
            {items.map((it) => (
              <button key={it.index} type="button" disabled={!action || !!busy || editing} onClick={() => run(action, { index: it.index }, `Go to ${it.title}`, String(it.index))}
                className={cn("relative flex w-full items-center gap-2 overflow-hidden rounded-lg border px-2.5 py-2 text-left text-sm transition active:scale-[0.99]",
                  it.index === cur ? "btn-sel font-bold" : "border-transparent bg-white/[0.025] hover:border-line-strong hover:bg-white/[0.04]")}>
                {it.index === cur && <span aria-hidden className="absolute inset-y-1.5 left-0 w-[3px] rounded-r bg-accent" />}
                <span className={cn("w-5 shrink-0 text-right font-mono text-[11px] tabular", it.index === cur ? "text-accent/80" : "text-dim")}>{it.index}</span>
                <span className="min-w-0 flex-1 truncate">{it.title}</span>
                {it.index === cur && <span className="btn-pgm shrink-0 rounded-full px-1.5 text-[9.5px] font-bold leading-[15px] tracking-[0.08em]">LIVE</span>}
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// ------------------------------------------------------------- library

type Size = WidgetDef["size"];
type Px = { w: number; h: number };

/** Per-kind defaults: desktop/tablet w×h (12 columns, 40px rows) and phone pw×ph (4 columns). */
const KIND_SIZE: Record<Exclude<WidgetKind, "core">, { size: Size; minPx: Px }> = {
  status: { size: { w: 4, h: 2, pw: 4, ph: 2 }, minPx: { w: 160, h: 60 } },
  list: { size: { w: 4, h: 8, pw: 4, ph: 8 }, minPx: { w: 180, h: 140 } },
  chat: { size: { w: 5, h: 10, pw: 4, ph: 10 }, minPx: { w: 260, h: 260 } },
  image: { size: { w: 6, h: 7, pw: 4, ph: 6 }, minPx: { w: 200, h: 150 } },
  buttons: { size: { w: 4, h: 3, pw: 4, ph: 3 }, minPx: { w: 140, h: 90 } },
  input: { size: { w: 4, h: 4, pw: 4, ph: 4 }, minPx: { w: 220, h: 130 } },
  strip: { size: { w: 8, h: 4, pw: 4, ph: 4 }, minPx: { w: 240, h: 120 } },
  sections: { size: { w: 4, h: 10, pw: 4, ph: 10 }, minPx: { w: 220, h: 160 } },
  text: { size: { w: 4, h: 3, pw: 4, ph: 3 }, minPx: { w: 160, h: 80 } },
  clock: { size: { w: 3, h: 4, pw: 2, ph: 3 }, minPx: { w: 150, h: 110 } },
  slider: { size: { w: 4, h: 3, pw: 4, ph: 3 }, minPx: { w: 120, h: 110 } },
  frame: { size: { w: 6, h: 6, pw: 4, ph: 6 }, minPx: { w: 200, h: 120 } },
};

const clamp = (v: unknown, lo: number, hi: number) => (typeof v === "number" && Number.isFinite(v) ? Math.min(hi, Math.max(lo, Math.round(v))) : null);

/** Grid size and minimum px: the manifest's when given (clamped), else the kind's. */
export function addonSize(w: AddonWidgetDef): { size: Size; minPx: Px } {
  const d = KIND_SIZE[w.kind as Exclude<WidgetKind, "core">] ?? KIND_SIZE.text;
  let size = d.size;
  const mw = clamp(w.size?.w, 1, 12), mh = clamp(w.size?.h, 1, 40);
  if (mw !== null && mh !== null) {
    // Phones have 4 columns: a widget a third of the desktop or more takes the full width.
    size = { w: mw, h: mh, pw: Math.min(4, Math.max(1, Math.ceil(mw / 3))), ph: mh };
  }
  const pw = clamp(w.minPx?.w, 40, 1200), ph = clamp(w.minPx?.h, 28, 2000);
  return { size, minPx: pw !== null && ph !== null ? { w: pw, h: ph } : d.minPx };
}

const warned = new Set<string>();

// One Render component per add-on widget type, kept for the life of the page.
// The add-on list is fetched again after every action (ptz:changed), and a new
// arrow function each time made React remount every add-on widget: frames
// reloaded mid-drag and lost the pointer. The component reads the newest
// manifest from `latest`, so updates still show.
const latest = new Map<string, { def: AddonWidgetDef; m: Manifest }>();
const renders = new Map<string, WidgetDef["Render"]>();
function stableRender(type: string): WidgetDef["Render"] {
  let r = renders.get(type);
  if (!r) {
    r = ({ s, editing, id, dash }) => {
      const x = latest.get(type);
      return x ? <AddonWidget def={x.def} s={s} manifest={x.m} editing={editing} widgetId={id} dash={dash} /> : null;
    };
    renders.set(type, r);
  }
  return r;
}

/** Widget-library entries for every add-on widget (registered as the add-ons load). */
export function addonWidgetDefs(manifests: Manifest[]): WidgetDef[] {
  const out: WidgetDef[] = [];
  for (const m of manifests) {
    for (const w of m.widgets ?? []) {
      if (w.kind === "core") continue;
      if (!(WIDGET_KINDS as readonly string[]).includes(w.kind)) {
        if (!warned.has(`${m.id}:${w.type}`)) console.warn(`add-on ${m.id}: widget ${w.type} has kind "${w.kind}", which this jadeck doesn't know; left out`);
        warned.add(`${m.id}:${w.type}`);
        continue;
      }
      const def = w;
      const type = `addon:${m.id}:${w.type}`;
      latest.set(type, { def, m });
      out.push({
        type, label: w.label, group: "addons", icon: Puzzle,
        description: `${m.name}: ${w.description ?? ""}`.trim(),
        ...addonSize(w),
        defaults: { ...fieldSettings(w.fields, {}), addon: m.id, widget: w.type },
        fields: [
          { key: "connection", label: "Connection", type: "connection", addon: m.id },
          // v2.1: the add-on's own settings for this widget, drawn like connection settings.
          ...(w.fields ?? []).map((f) => ({ key: f.key, label: f.label, type: "addon" as const, addon: m.id, addonField: f, help: f.help })),
        ],
        Render: stableRender(type),
      });
    }
  }
  return out;
}

/** Registers add-on widgets and returns them (re-renders when add-ons change). */
export function useAddonWidgets(): WidgetDef[] {
  const { data } = useShared<{ addons: { info: { enabled: boolean; manifest: Manifest } }[] }>("/api/addons", 0);
  const ms = (data?.addons ?? []).filter((a) => a.info.enabled).map((a) => a.info.manifest);
  const defs = addonWidgetDefs(ms);
  registerWidgets(defs);
  return defs;
}
