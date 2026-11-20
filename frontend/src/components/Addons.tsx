import { useRef, useState } from "react";
import { CheckCircle2, ChevronRight, FolderOpen, Loader2, RefreshCw, RotateCcw, ScrollText, ShieldAlert, Trash2, Upload, X } from "lucide-react";
import { api } from "@/lib/api";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { SampleData } from "@/lib/hooks";
import { useAddons, type AddonRow, type Manifest, type WidgetDef as AddonWidgetDef } from "@/lib/addons";
import { Button, Dialog, Input, Switch } from "@/components/ui";
import { FieldRow } from "@/components/FieldRow";
import { WIDGET } from "@/dash/widgets";
import { AddonSample, AddonWidget } from "@/dash/addonWidgets";

// Settings → Add-ons: a compact list; the panel on the right shows what
// an add-on adds, drawn by the real widgets with the add-on's sample data.

const CAP_STEPS: Record<string, string[]> = {
  assistant: [],
  switcher: ["Cut", "Fade", "Preview input"],
  slides: ["Next slide", "Previous slide", "Blank"],
  mixer: ["Mute / unmute channel"],
};
const CAP_TOOLS: Record<string, string[]> = {
  assistant: [],
  switcher: ["switcher_status", "switcher_preview", "switcher_cut", "switcher_fade"],
  slides: ["slides_status", "slides_action"],
  mixer: ["mixer_status", "mixer_mute"],
};
export const toolName = (addon: string, action: string) => `${addon.replace(/-/g, "_")}_${action}`;

export function AddonsSection() {
  const { data, reload } = useAddons(10000);
  const { log } = useLog();
  const [sel, setSel] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const [browse, setBrowse] = useState(false);
  const rows = data?.addons ?? [];
  const product = rows.filter((r) => !r.info.manifest.generic);
  const generic = rows.filter((r) => r.info.manifest.generic);
  const current = rows.find((r) => r.info.manifest.id === sel) ?? product[0] ?? rows[0];
  const check = async () => {
    setChecking(true);
    try { await api("/api/addons/check", "POST"); reload(); }
    catch (e) { log("Add-ons", (e as Error).message, false); }
    finally { setChecking(false); }
  };
  const row = (r: AddonRow) => {
    const m = r.info.manifest;
    const on = r.info.enabled;
    return (
      <button key={m.id} type="button" onClick={() => setSel(m.id)}
        className={cn("flex w-full items-baseline gap-2 rounded-xl border bg-panel px-3 py-2.5 text-left transition hover:border-line-strong",
          current?.info.manifest.id === m.id ? "border-accent" : "border-line")}>
        <span className="truncate text-sm font-bold">{m.name}</span>
        <span className="truncate text-[11px] text-muted">
          {m.version}{r.info.verified ? " · ✓" : " · not verified"}{r.info.source === "dev" ? " · dev" : ""}
        </span>
        {r.update && <span className="truncate text-[11px] text-info">· {r.update.version} ready</span>}
        <span className="ml-auto flex shrink-0 items-center gap-1.5 self-center">
          <span className={cn("h-2 w-2 rounded-full", !on ? "bg-dim" : r.info.lastError && !r.info.running && r.connections ? "bg-bad" : "bg-good")} />
          <ChevronRight size={14} className="text-dim" />
        </span>
      </button>
    );
  };
  return (
    <div className="grid gap-4 xl:grid-cols-[440px_minmax(0,1fr)]">
      <div className="min-w-0 space-y-2">
        <div className="flex items-center gap-2 pb-1">
          <h2 className="flex-1 text-xl font-bold">Add-ons</h2>
          <Button size="sm" variant="ghost" onClick={check} disabled={checking}>{checking ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />} Updates</Button>
          <Button size="sm" variant="accent" onClick={() => setBrowse(true)}>Browse</Button>
        </div>
        {data?.checkError && <p className="text-[11px] text-warn">{data.checkError}</p>}
        {product.map(row)}
        {generic.length > 0 && <h3 className="pt-2 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">Generic — for gear with no add-on yet</h3>}
        {generic.map(row)}
        {rows.length === 0 && <p className="rounded-xl border border-dashed border-line px-3 py-3 text-sm text-muted">No add-ons installed. Install one from a file below.</p>}
        <Developer onDone={reload} />
      </div>
      <div className="min-w-0">{current ? <Detail key={current.info.manifest.id} r={current} onChanged={reload} /> : null}</div>
      {browse && <Browse onClose={() => setBrowse(false)} onInstalled={(id) => { reload(); setSel(id); }} />}
    </div>
  );
}

function Detail({ r, onChanged }: { r: AddonRow; onChanged: () => void }) {
  const m = r.info.manifest;
  const { log } = useLog();
  const [busy, setBusy] = useState("");
  const [showLog, setShowLog] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const caps = [...new Set(m.connectionTypes.flatMap((t) => t.capabilities))];
  const steps = [...caps.flatMap((c) => CAP_STEPS[c] ?? []), ...(m.actions ?? []).filter((a) => a.macroStep).map((a) => a.label)];
  // A Controller add-on (the AI) uses the agent tools; it never becomes one.
  const tools = m.controller ? [] : [...caps.flatMap((c) => CAP_TOOLS[c] ?? []), ...(m.actions ?? []).map((a) => toolName(m.id, a.id))];
  const act = async (what: string, method: string, path: string, body?: unknown) => {
    setBusy(what);
    try { await api(path, method, body); log("Add-ons", `${m.name}: ${what}`); onChanged(); }
    catch (e) { log("Add-ons", `${m.name}: ${what} failed — ${(e as Error).message}`, false); }
    finally { setBusy(""); }
  };
  return (
    <div className="space-y-4 rounded-2xl border border-accent/60 bg-panel p-4">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h3 className="text-lg font-bold">{m.name} <span className="text-sm font-semibold text-muted">· {m.version}</span></h3>
          <p className="text-sm text-muted">{m.description}</p>
          <p className="mt-0.5 text-[11px] text-dim">
            {m.publisher}{r.info.verified ? " · ✓ verified" : " · not verified — signed by someone else or not at all"}
            {r.info.source === "dev" ? ` · dev folder ${r.info.path}` : ""}
          </p>
        </div>
        <label className="flex items-center gap-2 text-sm">{r.info.enabled ? "On" : "Off"}
          <Switch checked={r.info.enabled} onChange={(on) => act(on ? "turned on" : "turned off", "POST", `/api/addons/${m.id}/enabled`, { on })} />
        </label>
      </div>
      {r.update && (
        <div className="flex flex-wrap items-center gap-2 rounded-xl border border-info/50 bg-info/10 px-3 py-2 text-sm">
          <span className="flex-1">Version {r.update.version} is ready.</span>
          <Button size="sm" variant="accent" disabled={!!busy} onClick={() => act(`updated to ${r.update!.version}`, "POST", `/api/addons/${m.id}/update`)}>
            {busy.startsWith("updated") ? <Loader2 size={14} className="animate-spin" /> : null} Update
          </Button>
        </div>
      )}

      <section>
        <h4 className="mb-2 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">What you get</h4>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
          {(m.widgets ?? []).map((w) => <WidgetPreview key={w.type} m={m} w={w} />)}
          {m.connectionTypes.map((t) => (
            <figure key={t.id} className="space-y-1.5">
              <div className="pointer-events-none space-y-2 rounded-xl border border-line bg-bg p-3 select-none">
                {t.fields.filter((f) => !f.advanced).map((f) => (
                  <FieldRow key={f.key} f={{ ...f, help: undefined }} value={t.sample?.[f.key] ?? f.default} onChange={() => {}} />
                ))}
                {t.sampleTest && <p className="flex items-center gap-1.5 text-xs font-semibold text-good"><CheckCircle2 size={14} /> Test — {t.sampleTest}</p>}
              </div>
              <figcaption className="text-[11px] text-muted">New connection type: {t.label}</figcaption>
            </figure>
          ))}
        </div>
        {(steps.length > 0 || tools.length > 0) && (
          <div className="mt-3 flex flex-wrap gap-1.5 rounded-xl border border-line bg-bg p-2.5">
            {steps.map((s) => <span key={s} className="rounded-md bg-panel-2 px-2 py-1 text-xs">▶ {s}</span>)}
            {tools.map((t) => <span key={t} className="rounded-md bg-info/10 px-2 py-1 font-mono text-xs text-info">✦ {t}</span>)}
          </div>
        )}
        <p className="mt-1 text-[11px] text-dim">▶ macro steps · ✦ agent tools (MCP) — the same actions, from buttons, macros and agents.</p>
      </section>

      <section className="grid gap-3 sm:grid-cols-2">
        <div>
          <h4 className="mb-1 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">It can</h4>
          <p className="text-sm">{m.reaches || "The gear you add as connections"}.</p>
          <p className="text-[11px] text-dim">That's what it says it does. Verified add-ons are checked by the jadeck team.</p>
        </div>
        <div>
          <h4 className="mb-1 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">In use</h4>
          <p className="text-sm">{r.connections} connection{r.connections === 1 ? "" : "s"} · {r.widgets} widget{r.widgets === 1 ? "" : "s"} on {r.dashboards} dashboard{r.dashboards === 1 ? "" : "s"}</p>
          <p className="text-[11px] text-dim">{r.info.running ? `Running (pid ${r.info.pid})` : r.info.enabled ? "Starts when a connection uses it" : "Off"}{r.info.lastError ? ` · last error: ${r.info.lastError}` : ""}</p>
        </div>
      </section>

      <div className="flex flex-wrap items-center gap-2 rounded-xl bg-bg p-3 text-sm">
        {m.changelog && <span className="w-full text-xs text-muted">What's new in {m.version}: {m.changelog}</span>}
        <Button size="sm" variant="ghost" onClick={() => setShowLog(true)}><ScrollText size={14} /> Log</Button>
        <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => act("restarted", "POST", `/api/addons/${m.id}/restart`)}><RefreshCw size={14} /> Restart</Button>
        {r.info.canRollBack && <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => act("rolled back", "POST", `/api/addons/${m.id}/rollback`)}><RotateCcw size={14} /> Roll back</Button>}
        {r.info.source === "dev"
          ? <Button size="sm" variant="ghost" className="ml-auto" onClick={() => act("unloaded", "DELETE", `/api/addons/dev/${m.id}`)}><X size={14} /> Unload dev folder</Button>
          : <Button size="sm" variant="ghost" className="ml-auto text-bad" onClick={() => setConfirm(true)}><Trash2 size={14} /> Remove</Button>}
        <span className="w-full text-[11px] text-dim">Turning it off keeps its connections and widgets; they show that the add-on is off until you turn it back on.</span>
      </div>
      <Dialog open={confirm} onOpenChange={setConfirm} title={`Remove ${m.name}?`}
        description="Its connections and widgets stay saved but stop working until you install it again. To pause it instead, turn it off.">
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirm(false)}>Cancel</Button>
          <Button variant="danger" onClick={() => { setConfirm(false); act("removed", "DELETE", `/api/addons/${m.id}`); }}><Trash2 size={15} /> Remove</Button>
        </div>
      </Dialog>
      {showLog && <AddonLog id={m.id} name={m.name} onClose={() => setShowLog(false)} />}
    </div>
  );
}

function WidgetPreview({ m, w }: { m: Manifest; w: AddonWidgetDef }) {
  const core = w.kind === "core" ? WIDGET[w.type] : null;
  const tall = w.kind === "list" || w.kind === "chat" || w.type === "switcher.inputs" || w.type === "audio.favorites";
  let body: React.ReactNode;
  if (core) {
    const R = core.Render;
    body = <SampleData.Provider value={w.sample}><R s={{ ...core.defaults, ...(w.settings ?? {}) }} editing={false} /></SampleData.Provider>;
  } else if (w.kind !== "core") {
    body = <AddonSample.Provider value={w.sample}><AddonWidget def={w} s={{ addon: m.id }} /></AddonSample.Provider>;
  } else {
    body = <p className="p-3 text-xs text-dim">Needs a newer controller ({w.type}).</p>;
  }
  return (
    <figure className="space-y-1.5">
      <div className={cn("pointer-events-none overflow-hidden rounded-[var(--radius-card)] select-none", w.kind === "chat" ? "h-72" : tall ? "h-48" : "h-24")} aria-hidden>{body}</div>
      <figcaption className="text-[11px] text-muted">{w.label}{w.description ? ` — ${w.description}` : ""}</figcaption>
    </figure>
  );
}

function AddonLog({ id, name, onClose }: { id: string; name: string; onClose: () => void }) {
  const [entries, setEntries] = useState<{ time: string; level: string; msg: string }[] | null>(null);
  if (entries === null) api<{ entries: { time: string; level: string; msg: string }[] }>(`/api/addons/${id}/log`).then((r) => setEntries(r.entries)).catch(() => setEntries([]));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={`${name} — log`} wide>
      <div className="max-h-[60dvh] space-y-1 overflow-y-auto font-mono text-xs">
        {entries === null ? <p className="text-dim">Loading…</p> : entries.length === 0 ? <p className="text-dim">Nothing logged yet.</p> :
          entries.map((e, i) => (
            <p key={i} className={cn(e.level === "ERROR" ? "text-bad" : e.level === "WARN" ? "text-warn" : "text-muted")}>
              {new Date(e.time).toLocaleTimeString()} {e.msg}
            </p>
          ))}
      </div>
    </Dialog>
  );
}

function Developer({ onDone }: { onDone: () => void }) {
  const { log } = useLog();
  const file = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [path, setPath] = useState("");
  const [cmd, setCmd] = useState("go run .");
  const [unverified, setUnverified] = useState<{ data: ArrayBuffer; m: Manifest; detail: string } | null>(null);
  const upload = async (data: ArrayBuffer, allow = false) => {
    const r = await fetch(`/api/addons/install${allow ? "?allowUnverified=1" : ""}`, { method: "POST", body: data });
    const body = await r.json().catch(() => ({}));
    if (r.status === 409 && body.unverified) return setUnverified({ data, m: body.manifest, detail: body.detail });
    if (!r.ok) return log("Add-ons", `Install failed — ${body.detail ?? r.status}`, false);
    log("Add-ons", `${body.manifest.name} ${body.manifest.version} installed${body.verified ? "" : " (not verified)"}`);
    setUnverified(null);
    onDone();
  };
  return (
    <div className="rounded-xl border border-dashed border-line px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
        <span className="mr-auto">Developer</span>
        <Button size="sm" variant="ghost" onClick={() => file.current?.click()}><Upload size={14} /> Install from file</Button>
        <Button size="sm" variant="ghost" onClick={() => setOpen(true)}><FolderOpen size={14} /> Load a folder</Button>
        <a className="underline-offset-2 hover:text-fg hover:underline" href="https://github.com/toolingdev/jadeck/blob/main/ADDONS.md" target="_blank" rel="noreferrer">SDK</a>
      </div>
      <input ref={file} type="file" accept=".ptzaddon" hidden onChange={async (e) => {
        const f = e.target.files?.[0];
        e.target.value = "";
        if (f) upload(await f.arrayBuffer());
      }} />
      <Dialog open={open} onOpenChange={setOpen} title="Load an add-on folder (dev mode)"
        description="A project folder on this controller's computer with addon.json (from ptz-addon build) and the command that runs it. Agents can do this too over MCP.">
        <div className="space-y-3">
          <Input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/path/to/jadeck-addon-myproduct" />
          <Input value={cmd} onChange={(e) => setCmd(e.target.value)} placeholder="go run ." />
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setOpen(false)}>Cancel</Button>
            <Button variant="accent" onClick={async () => {
              try {
                await api("/api/addons/dev", "POST", { path, command: cmd.split(" ").filter(Boolean) });
                log("Add-ons", "Dev add-on loaded");
                setOpen(false);
                onDone();
              } catch (e) { log("Add-ons", (e as Error).message, false); }
            }}>Load</Button>
          </div>
        </div>
      </Dialog>
      <Dialog open={!!unverified} onOpenChange={(o) => !o && setUnverified(null)} title={`Install ${unverified?.m.name ?? ""} anyway?`}>
        <p className="mb-3 flex gap-2 text-sm text-warn"><ShieldAlert size={18} className="shrink-0" /> {unverified?.detail}</p>
        <p className="mb-3 text-xs text-muted">{unverified?.m.publisher} · {unverified?.m.version} · {unverified?.m.description}</p>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setUnverified(null)}>Cancel</Button>
          <Button variant="danger" onClick={() => unverified && upload(unverified.data, true)}>Install, I trust it</Button>
        </div>
      </Dialog>
    </div>
  );
}

type CatalogRow = { entry: { id: string; name: string; repo: string; publisher: string; description: string; category: string }; installed?: string };

/** Settings → Add-ons → Browse: the add-on list, one tap to install. */
function Browse({ onClose, onInstalled }: { onClose: () => void; onInstalled: (id: string) => void }) {
  const { log } = useLog();
  const [rows, setRows] = useState<CatalogRow[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [q, setQ] = useState("");
  const [unverified, setUnverified] = useState<{ id: string; detail: string } | null>(null);
  if (rows === null && !err) {
    api<{ addons: CatalogRow[] }>("/api/addons/catalog").then((r) => setRows(r.addons)).catch((e) => setErr((e as Error).message));
  }
  const install = async (id: string, allow = false) => {
    setBusy(id);
    try {
      const r = await fetch(`/api/addons/catalog/${id}/install${allow ? "?allowUnverified=1" : ""}`, { method: "POST" });
      const body = await r.json().catch(() => ({}));
      if (r.status === 409 && body.unverified) { setUnverified({ id, detail: body.detail }); return; }
      if (!r.ok) throw new Error(body.detail ?? `HTTP ${r.status}`);
      log("Add-ons", `${body.manifest.name} ${body.manifest.version} installed`);
      setUnverified(null);
      onInstalled(id);
      onClose();
    } catch (e) {
      log("Add-ons", `Install failed — ${(e as Error).message}`, false);
    } finally {
      setBusy("");
    }
  };
  const shown = (rows ?? []).filter((r) => !q || `${r.entry.name} ${r.entry.description} ${r.entry.category}`.toLowerCase().includes(q.toLowerCase()));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title="Browse add-ons" wide>
      <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search" className="mb-3" />
      {err && <p className="text-sm text-bad">{err}</p>}
      {rows === null && !err && <p className="text-sm text-dim">Loading…</p>}
      <div className="grid max-h-[60dvh] grid-cols-[repeat(auto-fill,minmax(230px,1fr))] gap-2 overflow-y-auto">
        {shown.map((r) => (
          <div key={r.entry.id} className="flex flex-col gap-1 rounded-xl border border-line bg-panel-2 p-3">
            <span className="text-sm font-bold">{r.entry.name}</span>
            <span className="text-[11px] text-muted">{r.entry.category} · {r.entry.publisher}{r.installed ? ` · ${r.installed} installed` : ""}</span>
            <span className="flex-1 text-xs text-muted">{r.entry.description}</span>
            {r.installed
              ? <span className="text-xs text-good">Installed</span>
              : <Button size="sm" variant="accent" className="self-start" disabled={!!busy} onClick={() => install(r.entry.id)}>
                  {busy === r.entry.id ? <Loader2 size={14} className="animate-spin" /> : null} Install
                </Button>}
          </div>
        ))}
      </div>
      <p className="mt-3 text-[11px] text-dim">Each add-on comes from its own GitHub releases and is checked against its signature before it installs.</p>
      <Dialog open={!!unverified} onOpenChange={(o) => !o && setUnverified(null)} title="Install anyway?">
        <p className="mb-3 flex gap-2 text-sm text-warn"><ShieldAlert size={18} className="shrink-0" /> {unverified?.detail}</p>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setUnverified(null)}>Cancel</Button>
          <Button variant="danger" onClick={() => unverified && install(unverified.id, true)}>Install, I trust it</Button>
        </div>
      </Dialog>
    </Dialog>
  );
}
