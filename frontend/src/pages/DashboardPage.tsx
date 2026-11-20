import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactGridLayout from "react-grid-layout";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import { Maximize2, SlidersHorizontal, Pencil, Undo2, Redo2, RotateCcw, Plus, Copy, Trash2, X, Check, ChevronDown, Star, Smartphone, Tablet, Monitor } from "lucide-react";
import { api } from "@/lib/api";
import { useLocal, useShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Badge, Button, Dialog, Input, Select } from "@/components/ui";
import { WIDGET, type WidgetDef, type Settings } from "@/dash/widgets";
import { WidgetView, Library, Inspector, SaveBadge } from "@/dash/editor";
import { useTouchDrag } from "@/dash/touchDrag";
import { useAddonWidgets } from "@/dash/addonWidgets";
import { enterFullScreen } from "@/lib/kiosk";
import { VideoCount, VIDEO_TYPES } from "@/dash/videoCount";
import { COLS, ROW_H, SCREENS, SCREEN_LABEL, screenFor, newId, bottomOf, stacked, twoUp, type Dashboard, type Defaults, type Screen } from "@/dash/types";

const MARGIN: [number, number] = [12, 12];
const PAD = 6;
const CANVAS_MAX: Record<Screen, string> = { phone: "max-w-[420px]", tablet: "max-w-[1100px]", desktop: "max-w-none" };
export const KIND_ICON = { phone: Smartphone, tablet: Tablet, desktop: Monitor };
const KIND_TONE: Record<Screen, "accent" | "info" | "good"> = { phone: "accent", tablet: "info", desktop: "good" };

/** Smallest cell count that still holds a widget's content at this
 *  column width: minimums are pixels, so they hold on any screen. */
function minFor(def: WidgetDef | undefined, cols: number, colW: number, s?: Settings) {
  const m = !def ? { w: 120, h: 80 } : typeof def.minPx === "function" ? def.minPx({ ...def.defaults, ...s }) : def.minPx;
  const w = colW > 0 ? Math.ceil((m.w + MARGIN[0]) / (colW + MARGIN[0])) : 1;
  const h = Math.ceil((m.h + MARGIN[1]) / (ROW_H + MARGIN[1]));
  return { w: Math.max(1, Math.min(cols, w)), h: Math.max(1, h) };
}

function sizeFor(def: WidgetDef, kind: Screen, colW = 0) {
  const cols = COLS[kind];
  const w = kind === "phone" ? (def.size.pw ?? cols) : def.size.w;
  const h = kind === "phone" ? (def.size.ph ?? def.size.h) : def.size.h;
  const m = minFor(def, cols, colW);
  return { w: Math.min(cols, Math.max(w, m.w)), h: Math.max(h, m.h) };
}

const colWidth = (width: number, cols: number, pad: number) => (width - pad * 2 - MARGIN[0] * (cols - 1)) / cols;

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v));
const plain = (d: Dashboard) => JSON.stringify({ ...d, layout: d.layout.map(({ i, x, y, w, h }) => ({ i, x, y, w, h })), updatedAt: "" });

function useWidth() {
  const [width, setWidth] = useState(0);
  const ro = useRef<ResizeObserver | null>(null);
  const el = useRef<HTMLDivElement | null>(null);
  const ref = useCallback((node: HTMLDivElement | null) => {
    ro.current?.disconnect();
    el.current = node;
    if (!node) return;
    setWidth(node.getBoundingClientRect().width);
    ro.current = new ResizeObserver(([e]) => setWidth(e.contentRect.width));
    ro.current.observe(node);
  }, []);
  return { width, ref, el, mounted: width > 0 };
}

export function KindBadge({ kind }: { kind: Screen }) {
  return <Badge tone={KIND_TONE[kind]}>{SCREEN_LABEL[kind]}</Badge>;
}

/** Which screen this device is (by viewport, not by the dashboard). */
function useDeviceScreen() {
  const read = () => ({ screen: screenFor(window.innerWidth, window.innerHeight), sideways: window.innerWidth > window.innerHeight });
  const [s, setS] = useState(read);
  useEffect(() => {
    const on = () => setS((o) => { const n = read(); return n.screen === o.screen && n.sideways === o.sideways ? o : n; });
    window.addEventListener("resize", on);
    return () => window.removeEventListener("resize", on);
  }, []);
  return s;
}

export function DashboardPage({ locked = false }: { locked?: boolean }) {
  useAddonWidgets(); // add-on widgets join the registry before widgets render
  const { log } = useLog();
  const { screen: device, sideways } = useDeviceScreen();

  const [list, setList] = useState<Dashboard[] | null>(null);
  const [defaults, setDefaults] = useState<Defaults>({});
  const [pick, setPick] = useLocal<number | null>("ptz.dashboard", null); // this device's choice
  const [draft, setDraft] = useState<Dashboard | null>(null);
  const [past, setPast] = useState<Dashboard[]>([]);
  const [future, setFuture] = useState<Dashboard[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [saveState, setSaveState] = useState<"saved" | "pending" | "saving" | "error">("saved");
  const [dialog, setDialog] = useState<null | "picker" | "new" | "delete">(null);
  const [newCopyOf, setNewCopyOf] = useState<number | null>(null);
  const [sheet, setSheet] = useState(false);
  const lastKey = useRef<{ key: string; at: number } | null>(null);
  const snapshot = useRef<Dashboard | null>(null);

  const load = useCallback(async () => {
    try {
      const [l, d] = await Promise.all([api<Dashboard[]>("/api/dashboards"), api<Defaults>("/api/dashboards/defaults")]);
      setList(l);
      setDefaults(d);
      return l;
    } catch (e) {
      log("Dashboards", (e as Error).message, false);
      return null;
    }
  }, [log]);
  useEffect(() => {
    load();
  }, [load]);

  // This device's pick, else the default for this kind of screen.
  const current = useMemo(() => {
    if (!list) return null;
    return list.find((d) => d.id === pick) ?? list.find((d) => d.id === defaults[device]) ?? list[0] ?? null;
  }, [list, pick, defaults, device]);
  const shown = draft ?? current;
  const editing = draft !== null;

  const { width, ref: containerRef, el: boxEl, mounted } = useWidth();

  // How the dashboard sits on this screen.
  const kind = shown?.kind ?? "tablet";
  const view = useMemo(() => {
    if (!shown) return { cols: 12, layout: [], max: "max-w-none", note: "" };
    if (editing) return { cols: COLS[kind], layout: shown.layout, max: CANVAS_MAX[kind], note: "" };
    // A phone on its side: two columns instead of one long scroll.
    if (device === "phone" && sideways) return { cols: 8, layout: twoUp(shown.layout, 4), max: "max-w-none", note: "two columns sideways" };
    if (kind === "phone" && device !== "phone") return { cols: 4, layout: shown.layout, max: "max-w-[420px]", note: "phone dashboard" };
    if (kind !== "phone" && device === "phone") return { cols: 4, layout: stacked(shown.layout, 4), max: "max-w-none", note: "stacked for this phone" };
    return { cols: COLS[kind], layout: shown.layout, max: "max-w-none", note: "" };
  }, [shown, editing, kind, device, sideways]);
  const colW = colWidth(width, view.cols, editing ? PAD : 0);
  // Measured height each widget needs to show all its contents.
  const [need, setNeed] = useState<Record<string, number>>({});
  const onNeed = useCallback((id: string, px: number | null) => setNeed((n) => {
    if (px === null) {
      if (!(id in n)) return n;
      const { [id]: _, ...rest } = n;
      return rest;
    }
    return n[id] === px ? n : { ...n, [id]: px };
  }), []);

  // Settings → Dashboards → Edit lands here with a flag.
  useEffect(() => {
    if (!current || editing) return;
    try {
      if (localStorage.getItem("ptz.dashboard.editNow") === String(current.id)) {
        localStorage.removeItem("ptz.dashboard.editNow");
        startEdit(current);
      }
    } catch { /* ignore */ }
  }, [current]); // eslint-disable-line react-hooks/exhaustive-deps

  // ------------------------------------------------------ history + edit
  const commit = (next: Dashboard, key?: string) => {
    const now = Date.now();
    const coalesce = key && lastKey.current?.key === key && now - lastKey.current.at < 1500;
    lastKey.current = key ? { key, at: now } : null;
    if (!coalesce) setPast((h) => [...h.slice(-99), clone(draft!)]);
    setFuture([]);
    setDraft(next);
  };
  const startEdit = (d: Dashboard) => {
    snapshot.current = clone(d);
    savedJSON.current = plain(d);
    setDraft(clone(d));
    setPast([]);
    setFuture([]);
    setSaveState("saved");
    setSelected(null);
  };
  const undo = () => {
    if (!past.length || !draft) return;
    lastKey.current = null;
    setFuture((f) => [...f, clone(draft)]);
    setDraft(past[past.length - 1]);
    setPast(past.slice(0, -1));
  };
  const redo = () => {
    if (!future.length || !draft) return;
    lastKey.current = null;
    setPast((h) => [...h, clone(draft)]);
    setDraft(future[future.length - 1]);
    setFuture(future.slice(0, -1));
  };
  const revertAll = () => {
    if (snapshot.current && draft) commit(clone(snapshot.current));
  };

  // Autosave ~0.7 s after a change; retry while unreachable.
  const savedJSON = useRef("");
  const saveSeq = useRef(0);
  const doSave = useCallback(async (d: Dashboard) => {
    const json = plain(d);
    if (json === savedJSON.current) {
      setSaveState("saved");
      return true;
    }
    const seq = ++saveSeq.current;
    setSaveState("saving");
    try {
      const back = await api<Dashboard>(`/api/dashboards/${d.id}`, "PUT", JSON.parse(json));
      if (seq !== saveSeq.current) return true;
      savedJSON.current = json;
      setList((l) => (l ? l.map((x) => (x.id === back.id ? back : x)) : l));
      setSaveState("saved");
      return true;
    } catch (e) {
      if (seq === saveSeq.current) setSaveState("error");
      log("Dashboards", `Not saved yet — ${(e as Error).message}`, false);
      return false;
    }
  }, [log]);
  useEffect(() => {
    if (!draft || plain(draft) === savedJSON.current) return;
    setSaveState("pending");
    const t = window.setTimeout(() => doSave(draft), 700);
    return () => clearTimeout(t);
  }, [draft, doSave]);
  useEffect(() => {
    if (saveState !== "error" || !draft) return;
    const t = window.setTimeout(() => doSave(draft), 3000);
    return () => clearTimeout(t);
  }, [saveState, draft, doSave]);
  useEffect(() => {
    if (!draft) return;
    const flush = () => document.hidden && doSave(draft);
    document.addEventListener("visibilitychange", flush);
    return () => document.removeEventListener("visibilitychange", flush);
  }, [draft, doSave]);
  useEffect(() => {
    if (!draft) return;
    const onKey = (e: KeyboardEvent) => {
      const t = e.target;
      if (t instanceof Element && t.closest("input, textarea, select, [contenteditable]")) return;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "z") {
        e.preventDefault();
        if (e.shiftKey) redo();
        else undo();
      } else if (mod && e.key.toLowerCase() === "y") {
        e.preventDefault();
        redo();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const done = async () => {
    if (!draft) return;
    if (await doSave(draft)) {
      log("Dashboards", `Saved “${draft.name}”`);
      setDraft(null);
      setSelected(null);
    }
  };

  // --------------------------------------------------------- widgets
  const addWidget = (def: WidgetDef, at?: { x: number; y: number }) => {
    if (!draft) return;
    const next = clone(draft);
    const id = newId(def.type);
    next.widgets.push({ id, type: def.type, settings: clone(def.defaults) });
    next.layout.push({ i: id, ...(at ?? { x: 0, y: bottomOf(next.layout) }), ...sizeFor(def, next.kind, colW) });
    commit(next);
    setSelected(id);
  };
  const removeWidget = (id: string) => {
    if (!draft) return;
    const next = clone(draft);
    next.widgets = next.widgets.filter((w) => w.id !== id);
    next.layout = next.layout.filter((it) => it.i !== id);
    commit(next);
    setSelected(null);
  };
  const duplicateWidget = (id: string) => {
    if (!draft) return;
    const w = draft.widgets.find((x) => x.id === id);
    const src = draft.layout.find((it) => it.i === id);
    if (!w) return;
    const next = clone(draft);
    const nid = newId(w.type);
    next.widgets.push({ id: nid, type: w.type, settings: clone(w.settings) });
    next.layout.push({ i: nid, x: 0, y: bottomOf(next.layout), w: src?.w ?? 3, h: src?.h ?? 3 });
    commit(next);
    setSelected(nid);
  };
  const setSettings = (id: string, settings: Settings) => {
    if (!draft) return;
    const next = clone(draft);
    const w = next.widgets.find((x) => x.id === id);
    if (w) w.settings = settings;
    commit(next);
  };
  const gesture = useRef(false);
  const onLayout = (layout: readonly { i: string; x: number; y: number; w: number; h: number }[]) => {
    if (!draft) return;
    const items = layout.filter((it) => it.i !== "__drop__").map(({ i, x, y, w, h }) => ({ i, x, y, w, h }));
    const user = gesture.current;
    gesture.current = false;
    if (JSON.stringify(items) === JSON.stringify(draft.layout.map(({ i, x, y, w, h }) => ({ i, x, y, w, h })))) return;
    const next = { ...clone(draft), layout: items };
    if (user) commit(next);
    else setDraft(next);
  };

  // ---------------------------------------------------- touch drag
  const cellAt = (def: WidgetDef, cx: number, cy: number) => {
    const box = boxEl.current;
    if (!box || !draft) return null;
    const r = box.getBoundingClientRect();
    if (cx < r.left || cx > r.right || cy < r.top || cy > r.bottom + 40) return null;
    const cols = COLS[draft.kind];
    const colW = (r.width - PAD * 2 - MARGIN[0] * (cols - 1)) / cols;
    const sz = sizeFor(def, draft.kind, colW);
    const gx = Math.round((cx - r.left - PAD - (sz.w * (colW + MARGIN[0])) / 2) / (colW + MARGIN[0]));
    const gy = Math.round((cy - r.top - PAD - (sz.h * (ROW_H + MARGIN[1])) / 2) / (ROW_H + MARGIN[1]));
    return { x: Math.max(0, Math.min(cols - sz.w, gx)), y: Math.max(0, gy), ...sz, colW };
  };
  const touch = useTouchDrag<WidgetDef>((def, cx, cy) => {
    const c = cellAt(def, cx, cy);
    setSheet(false);
    if (c) addWidget(def, { x: c.x, y: c.y });
  });
  const dropCell = touch.drag ? cellAt(touch.drag.item, touch.drag.x, touch.drag.y) : null;
  const [dragType, setDragType] = useState<string | null>(null);
  const dragDef = dragType ? WIDGET[dragType] : null;

  // ------------------------------------------------- dashboards
  const deleteDash = async () => {
    if (!current) return;
    try {
      await api(`/api/dashboards/${current.id}`, "DELETE");
      setDraft(null);
      setDialog(null);
      setPick(null);
      await load();
      log("Dashboards", `Deleted “${current.name}”`);
    } catch (e) {
      log("Dashboards", (e as Error).message, false);
    }
  };
  const created = async (d: Dashboard) => {
    setDialog(null);
    await load();
    setPick(d.id);
    startEdit(d);
  };

  const gridLayout = useMemo(() => view.layout.map((it) => {
    const w = shown?.widgets.find((x) => x.id === it.i);
    const def = w && WIDGET[w.type];
    // Never smaller than the content needs: grow a too-small saved cell
    // (an old layout, or a big dashboard on a narrower screen) and stop
    // the resize handle at the minimum.
    const m = minFor(def, view.cols, colW, w?.settings);
    // …and never shorter than what shows everything in it: 8 presets or
    // 20, the box grows to fit them rather than growing a scrollbar.
    const fit = need[it.i] ? Math.ceil((need[it.i] + MARGIN[1]) / (ROW_H + MARGIN[1])) : 0;
    const minH = Math.max(m.h, fit);
    return { ...it, w: Math.min(view.cols, Math.max(it.w, m.w)), h: Math.max(it.h, minH), minW: m.w, minH };
  }), [view, shown, colW, need]);

  // Outside edit mode: a Video widget switches feed in place; another
  // video already showing that feed takes this one's (they swap).
  const switchFeed = async (id: string, feed: string) => {
    if (!current) return;
    const next = clone(current);
    const me = next.widgets.find((x) => x.id === id);
    if (!me) return;
    const old = me.settings.feed;
    const other = next.widgets.find((x) => x.id !== id && x.type === "video" && x.settings.feed === feed);
    me.settings = { ...me.settings, feed };
    if (other) other.settings = { ...other.settings, feed: old };
    setList((l) => (l ? l.map((x) => (x.id === next.id ? next : x)) : l));
    try {
      const back = await api<Dashboard>(`/api/dashboards/${next.id}`, "PUT", JSON.parse(plain(next)));
      setList((l) => (l ? l.map((x) => (x.id === back.id ? back : x)) : l));
    } catch (e) {
      log("Dashboards", (e as Error).message, false);
      load();
    }
  };

  if (!list) return <p className="p-6 text-sm text-muted">Loading dashboards…</p>;
  if (!shown) return <p className="p-6 text-sm text-muted">No dashboards.</p>;
  const isDefaultFor = SCREENS.filter((s) => defaults[s] === shown.id);
  const selectedWidget = draft?.widgets.find((w) => w.id === selected) ?? null;
  const KindIcon = KIND_ICON[kind];

  return (
    <div className={cn("flex gap-3", editing && "2xl:items-start")}>
      {editing && <Library onAdd={(d) => !touch.swallowClick() && addWidget(d)} onDragType={setDragType} onTouchStart={touch.start} />}

      <div className="min-w-0 flex-1">
        <div className={cn("mb-3 flex flex-wrap items-center gap-2 short:mb-1.5", locked && "hidden")}>
          {editing ? (
            <>
              <Input value={draft!.name} onChange={(e) => commit({ ...draft!, name: e.target.value }, "name")} className="h-9 w-40 sm:w-48" aria-label="Dashboard name" />
              <Badge tone="accent">Editing</Badge>
              <KindBadge kind={kind} />
              <Button size="sm" variant="accent" className="2xl:hidden" onClick={() => setSheet(true)}><Plus size={14} /> Add widget</Button>
              <SaveBadge state={saveState} onRetry={() => draft && doSave(draft)} />
              <div className="ml-auto flex items-center gap-1">
                <Button size="sm" variant="ghost" onClick={undo} disabled={!past.length} aria-label="Undo" title="Undo (⌘Z)"><Undo2 size={15} /><span className="max-sm:hidden">Undo</span></Button>
                <Button size="sm" variant="ghost" onClick={redo} disabled={!future.length} aria-label="Redo" title="Redo (⇧⌘Z)"><Redo2 size={15} /><span className="max-sm:hidden">Redo</span></Button>
                <Button size="sm" variant="accent" onClick={done} disabled={saveState === "saving"}><Check size={14} /> Done</Button>
              </div>
            </>
          ) : (
            <>
              <button type="button" onClick={() => setDialog("picker")}
                className="flex h-9 items-center gap-2 rounded-xl border border-line bg-panel px-3 text-sm font-semibold hover:border-line-strong">
                <KindIcon size={15} className="text-muted" />
                {shown.name}
                {isDefaultFor.includes(device) && <Star size={12} className="fill-accent text-accent" />}
                <ChevronDown size={15} className="text-muted" />
              </button>
              <span className="flex items-center gap-1 text-xs text-dim short:hidden">Layout locked{view.note && ` · ${view.note}`}</span>
              <div className="ml-auto flex items-center gap-2">
                <Button size="sm" variant="ghost" onClick={enterFullScreen} title="Only the dashboard, and the screen stays awake. The ✕ in the corner (tap twice) or Esc leaves.">
                  <Maximize2 size={14} /> Full screen
                </Button>
                <Button size="sm" onClick={() => startEdit(shown)}><Pencil size={14} /> Edit</Button>
              </div>
            </>
          )}
        </div>

        {editing && (
          <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-dim">
            <span>
              {device !== kind && COLS[device] < COLS[kind]
                ? `This is a ${SCREEN_LABEL[kind].toLowerCase()} dashboard — it's easier to arrange on a ${SCREEN_LABEL[kind].toLowerCase()}. `
                : ""}
              Drag widgets in (press and hold on touch screens). Drag to move, pull the corner to resize. Changes save themselves.
            </span>
            <span className="ml-auto flex gap-1">
              <Button size="sm" variant="ghost" onClick={revertAll} disabled={!past.length}><RotateCcw size={13} /> Back to how it was</Button>
              <Button size="sm" variant="ghost" onClick={() => { setNewCopyOf(draft!.id); setDialog("new"); }}><Copy size={13} /> Duplicate</Button>
              <Button size="sm" variant="ghost" className="text-bad" onClick={() => setDialog("delete")}
                disabled={list.length <= 1 || isDefaultFor.length > 0}
                title={isDefaultFor.length ? `Default for ${isDefaultFor.join(", ")} — pick another default in Settings first` : undefined}>
                <Trash2 size={13} /> Delete
              </Button>
            </span>
          </div>
        )}

        <div className={cn("mx-auto w-full", view.max)}>
          <div ref={containerRef}
            className={cn("relative min-h-[60dvh] rounded-2xl", editing && "dash-edit border border-dashed border-line bg-[linear-gradient(90deg,rgba(255,255,255,.025)_1px,transparent_1px)]")}
            style={editing && mounted ? { backgroundSize: `${(width - MARGIN[0]) / view.cols}px 100%`, backgroundPosition: `${MARGIN[0] / 2}px 0` } : undefined}
            onClick={(e) => { if (e.target === e.currentTarget) setSelected(null); }}>
            {mounted && (
              <VideoCount.Provider value={shown.widgets.filter((w) => VIDEO_TYPES.has(w.type)).length}>
              <ReactGridLayout
                width={width}
                layout={gridLayout}
                gridConfig={{ cols: view.cols, rowHeight: ROW_H, margin: MARGIN, containerPadding: editing ? [PAD, PAD] : [0, 0] }}
                dragConfig={{ enabled: editing, threshold: 4, cancel: ".no-drag" }}
                resizeConfig={{ enabled: editing, handles: ["se"] }}
                dropConfig={{ enabled: editing, defaultItem: dragDef ? sizeFor(dragDef, kind, colW) : { w: 3, h: 3 } }}
                droppingItem={{ i: "__drop__", x: 0, y: 0, ...(dragDef ? sizeFor(dragDef, kind, colW) : { w: 3, h: 3 }) }}
                onLayoutChange={editing ? onLayout : undefined}
                onDragStop={() => (gesture.current = true)}
                onResizeStop={() => (gesture.current = true)}
                onDrop={(_l, item) => {
                  const def = dragType ? WIDGET[dragType] : null;
                  setDragType(null);
                  if (def && item) addWidget(def, { x: item.x, y: item.y });
                }}
              >
                {shown.widgets.map((w) => (
                  <div key={w.id} className={cn("relative", editing && "cursor-grab active:cursor-grabbing")}>
                    <WidgetView w={w} editing={editing} onNeed={onNeed} />
                    {!editing && !locked && w.type === "video" && w.settings?.switchHere !== false && typeof w.settings?.feed === "string" && w.settings.feed && (
                      <FeedSwitch value={w.settings.feed as string} onPick={(v) => switchFeed(w.id, v)} />
                    )}
                    {editing && (
                      <div className={cn("absolute inset-0 z-10 rounded-[var(--radius-card)] transition", selected === w.id ? "ring-2 ring-accent" : "hover:ring-1 hover:ring-line-strong")}
                        onClick={() => setSelected(w.id)} aria-label={`Select ${WIDGET[w.type]?.label ?? w.type}`}>
                        <NameChip w={w} onOpen={() => setSelected(w.id)} />
                      </div>
                    )}
                  </div>
                ))}
              </ReactGridLayout>
              </VideoCount.Provider>
            )}
            {dropCell && (
              <div className="pointer-events-none absolute z-20 rounded-[var(--radius-card)] border-2 border-dashed border-accent bg-accent/10"
                style={{
                  left: PAD + dropCell.x * (dropCell.colW + MARGIN[0]), top: PAD + dropCell.y * (ROW_H + MARGIN[1]),
                  width: dropCell.w * dropCell.colW + (dropCell.w - 1) * MARGIN[0], height: dropCell.h * ROW_H + (dropCell.h - 1) * MARGIN[1],
                }}>
                <span className="absolute inset-0 flex items-center justify-center text-xs font-semibold text-accent">Lands here</span>
              </div>
            )}
            {shown.widgets.length === 0 && (
              <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-6 text-center text-sm text-dim">
                {editing ? "Drag a widget here to start." : "This dashboard is empty — press Edit to add widgets."}
              </div>
            )}
          </div>
        </div>
      </div>

      {editing && selectedWidget && (
        <Inspector w={selectedWidget} onChange={(s) => setSettings(selectedWidget.id, s)}
          onRemove={() => removeWidget(selectedWidget.id)} onDuplicate={() => duplicateWidget(selectedWidget.id)}
          onClose={() => setSelected(null)} pos={draft!.layout.find((it) => it.i === selectedWidget.id)} />
      )}

      {sheet && (
        <div className={cn("fixed inset-0 z-50 transition-opacity", touch.drag && "pointer-events-none opacity-0")}>
          <div className="absolute inset-0 bg-black/60" onClick={() => setSheet(false)} />
          <div className="absolute inset-x-0 bottom-0 max-h-[70dvh] overflow-y-auto rounded-t-3xl border border-line bg-panel p-4 pb-[max(1rem,env(safe-area-inset-bottom))] shadow-2xl sm:mx-auto sm:max-w-xl">
            <div className="mx-auto mb-3 h-1 w-10 rounded-full bg-line-strong" />
            <div className="mb-1 flex items-center">
              <span className="text-base font-semibold">Add a widget</span>
              <Button size="icon" variant="ghost" className="ml-auto h-8 w-8" onClick={() => setSheet(false)} aria-label="Close"><X size={16} /></Button>
            </div>
            <p className="mb-3 text-xs text-muted">Tap to add at the bottom — or press and drag it onto the grid.</p>
            <Library inline onAdd={(d) => { if (touch.swallowClick()) return; addWidget(d); setSheet(false); }} onDragType={() => {}} onTouchStart={touch.start} />
          </div>
        </div>
      )}
      {touch.drag && (
        <div className="pointer-events-none fixed z-[60] -translate-x-1/2 -translate-y-1/2 -rotate-3 rounded-xl border-2 border-accent bg-panel-2/95 px-3 py-2 text-sm font-semibold text-accent shadow-[0_16px_40px_rgba(0,0,0,.7)]"
          style={{ left: touch.drag.x, top: touch.drag.y - 36 }}>
          <touch.drag.item.icon size={14} className="mr-1.5 inline" />{touch.drag.item.label}
        </div>
      )}

      <Picker open={dialog === "picker"} onClose={() => setDialog(null)} list={list} defaults={defaults} device={device} currentId={shown.id}
        onPick={(id) => { setPick(id); setDialog(null); }} onNew={() => { setNewCopyOf(null); setDialog("new"); }} />
      <NewDashboard open={dialog === "new"} onClose={() => setDialog(null)} list={list} device={device} copyOf={newCopyOf} onCreated={created} />
      <Dialog open={dialog === "delete"} onOpenChange={(o) => !o && setDialog(null)} title={`Delete “${shown.name}”?`} description="Everyone loses this dashboard. This can't be undone.">
        <div className="flex justify-end gap-2"><Button variant="ghost" onClick={() => setDialog(null)}>Cancel</Button><Button variant="danger" onClick={deleteDash}>Delete</Button></div>
      </Dialog>
    </div>
  );
}

// ------------------------------------------------------------------ picker

function Picker({ open, onClose, list, defaults, device, currentId, onPick, onNew }: {
  open: boolean; onClose: () => void; list: Dashboard[]; defaults: Defaults; device: Screen; currentId: number;
  onPick: (id: number) => void; onNew: () => void;
}) {
  const mine = list.filter((d) => d.kind === device);
  const others = list.filter((d) => d.kind !== device);
  const row = (d: Dashboard) => {
    const defFor = SCREENS.filter((s) => defaults[s] === d.id);
    const Icon = KIND_ICON[d.kind];
    return (
      <button key={d.id} type="button" onClick={() => onPick(d.id)}
        className={cn("flex w-full items-center gap-3 rounded-xl border px-3 py-2.5 text-left transition",
          d.id === currentId ? "border-accent bg-accent/10" : "border-line bg-panel-2 hover:border-line-strong")}>
        <Icon size={16} className="shrink-0 text-muted" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-semibold">{d.name}</span>
          <span className="block text-[11px] text-dim">
            {defFor.length ? <span className="text-accent">★ default for {defFor.map((s) => SCREEN_LABEL[s].toLowerCase() + "s").join(", ")} · </span> : null}
            {d.widgets.length} widgets{d.kind !== device ? (device === "phone" ? " · stacks on a phone" : d.kind === "phone" ? " · shown phone-width" : "") : ""}
          </span>
        </span>
        <KindBadge kind={d.kind} />
        {d.id === currentId && <Check size={16} className="text-accent" />}
      </button>
    );
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title="Dashboards" description="This device remembers your pick. Defaults are set in Settings.">
      <div className="space-y-3">
        {mine.length > 0 && (
          <div>
            <div className="mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">Made for {SCREEN_LABEL[device].toLowerCase()}s</div>
            <div className="space-y-1.5">{mine.map(row)}</div>
          </div>
        )}
        {others.length > 0 && (
          <div>
            <div className="mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">Other dashboards — work here too</div>
            <div className="space-y-1.5">{others.map(row)}</div>
          </div>
        )}
        <Button className="w-full" onClick={onNew}><Plus size={15} /> New dashboard</Button>
      </div>
    </Dialog>
  );
}

// ------------------------------------------------------------ new dialog

export function NewDashboard({ open, onClose, list, device, copyOf, onCreated }: {
  open: boolean; onClose: () => void; list: Dashboard[]; device: Screen; copyOf: number | null; onCreated: (d: Dashboard) => void;
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<Screen>(device);
  const [from, setFrom] = useState<string>("");
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!open) return;
    const src = list.find((d) => d.id === copyOf);
    setName(src ? `${src.name} copy` : "");
    setKind(src?.kind ?? device);
    setFrom(src ? String(src.id) : "");
    setErr("");
  }, [open, copyOf, device, list]);
  const create = async () => {
    try {
      const body = from ? { name, kind, from: Number(from) } : { name: name || "New dashboard", kind, widgets: [], layout: [] };
      onCreated(await api<Dashboard>("/api/dashboards", "POST", body));
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title={copyOf ? "Duplicate dashboard" : "New dashboard"}>
      <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); create(); }}>
        <label className="block"><span className="mb-1 block text-xs text-muted">Name</span>
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Speaker's lectern" maxLength={60} />
        </label>
        <div>
          <span className="mb-1 block text-xs text-muted">Made for</span>
          <div className="grid grid-cols-3 gap-2">
            {SCREENS.map((s) => {
              const Icon = KIND_ICON[s];
              return (
                <button key={s} type="button" onClick={() => setKind(s)}
                  className={cn("flex h-16 flex-col items-center justify-center gap-1 rounded-xl border text-xs font-semibold transition",
                    kind === s ? "border-accent bg-accent/10 text-fg" : "border-line bg-panel-2 text-muted hover:text-fg")}>
                  <Icon size={18} />{SCREEN_LABEL[s]}
                </button>
              );
            })}
          </div>
          <span className="mt-1 block text-[11px] text-dim">Sets the grid (phone 4 columns, tablet/desktop 12). It still works on every device.</span>
        </div>
        <label className="block"><span className="mb-1 block text-xs text-muted">Start from</span>
          <Select value={from} onChange={setFrom} options={[{ value: "", label: "Empty" }, ...list.map((d) => ({ value: String(d.id), label: `Copy of ${d.name} (${SCREEN_LABEL[d.kind]})` }))]} />
          {from && list.find((d) => String(d.id) === from)?.kind !== kind && <span className="mt-1 block text-[11px] text-dim">The copy's widgets are re-arranged for a {SCREEN_LABEL[kind].toLowerCase()}.</span>}
        </label>
        {err && <p className="text-sm text-bad">{err}</p>}
        <div className="flex justify-end gap-2"><Button variant="ghost" onClick={onClose}>Cancel</Button><Button type="submit" variant="accent">Create</Button></div>
      </form>
    </Dialog>
  );
}

/** Edit mode: what a widget is, on the widget itself. Tapping it (or
 *  the widget) opens its settings. */
function NameChip({ w, onOpen }: { w: { id: string; type: string; settings?: Settings }; onOpen: () => void }) {
  const def = WIDGET[w.type];
  const s = (w.settings ?? {}) as Record<string, unknown>;
  const feeds = useFeedNames();
  const cams = useCameraNames();
  const detail = typeof s.channel === "string" ? s.channel
    : Array.isArray(s.channels) && s.channels.length ? (s.channels as string[]).join(", ")
    : typeof s.feed === "string" && s.feed ? (s.feed === "cam:" ? "header's camera" : feeds[s.feed] ?? s.feed)
    : typeof s.camera === "string" && s.camera ? cams[s.camera] ?? "camera"
    : def?.group === "camera" ? "header's camera" : "";
  return (
    <div className="no-drag pointer-events-none absolute left-2 top-2 z-20 max-w-[calc(100%-1rem)]">
      <button type="button" onClick={(e) => { e.stopPropagation(); onOpen(); }} onPointerDown={(e) => e.stopPropagation()}
        className="pointer-events-auto flex max-w-full items-center gap-1.5 rounded-full border border-accent bg-bg/90 px-2.5 py-1 text-[11px] font-semibold text-accent shadow backdrop-blur">
        <span className="truncate">{def?.label ?? w.type}{detail && <span className="font-medium text-accent/80"> · {detail}</span>}</span>
        <SlidersHorizontal size={12} className="shrink-0" />
      </button>
    </div>
  );
}

function useFeedNames() {
  const { data } = useShared<{ feeds: { id: string; name: string }[] }>("/api/feeds", 0);
  return Object.fromEntries((data?.feeds ?? []).map((f) => [f.id, f.name])) as Record<string, string>;
}

function useCameraNames() {
  const { data } = useShared<{ id: string; name: string }[]>("/api/cameras", 0);
  return Object.fromEntries((data ?? []).map((c) => [c.id, c.name])) as Record<string, string>;
}

/** Outside edit mode: the feed's name on a Video widget; tap to switch. */
function FeedSwitch({ value, onPick }: { value: string; onPick: (v: string) => void }) {
  const { data } = useShared<{ feeds: { id: string; name: string }[] }>("/api/feeds", 0);
  const feeds = data?.feeds ?? [];
  const [open, setOpen] = useState(false);
  const name = value === "cam:" ? "Header's camera" : feeds.find((f) => f.id === value)?.name ?? "Feed";
  if (feeds.length < 2) return null;
  return (
    <div className="absolute bottom-2 left-2 z-20">
      {open && <div className="fixed inset-0 z-0" onClick={() => setOpen(false)} />}
      {open && (
        <div className="absolute bottom-9 left-0 z-10 min-w-44 rounded-xl border border-line bg-panel-2 p-1 shadow-2xl">
          {feeds.map((f) => (
            <button key={f.id} type="button" onClick={() => { setOpen(false); if (f.id !== value) onPick(f.id); }}
              className={cn("flex w-full items-center gap-2 whitespace-nowrap rounded-lg px-2.5 py-2 text-left text-sm", f.id === value ? "text-accent" : "text-fg hover:bg-raise")}>
              <span className="w-3">{f.id === value ? "✓" : ""}</span>{f.name}
            </button>
          ))}
        </div>
      )}
      <button type="button" onClick={() => setOpen(!open)}
        className="relative z-10 flex items-center gap-1 rounded-lg bg-black/60 px-2 py-1 text-[11px] font-semibold text-white backdrop-blur hover:bg-black/80">
        {name} <ChevronDown size={12} />
      </button>
    </div>
  );
}
