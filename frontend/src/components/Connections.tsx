import { useMemo, useState } from "react";
import { CheckCircle2, Loader2, Plus, Save, Search, Trash2, X, XCircle } from "lucide-react";
import { api } from "@/lib/api";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { CAP_LABEL, defaults, useAddons, useConnections, type Capability, type Connection, type ConnectionType, type Fav, type Manifest } from "@/lib/addons";
import { Button, Dialog, Input } from "@/components/ui";
import { FieldRow, QuickChannels } from "@/components/FieldRow";

// Settings → Connections: the gear this controller talks to, any number of
// each kind, each through an add-on's connection type.

const CAPS: Capability[] = ["switcher", "slides", "mixer", "assistant"];

type Draft = { id?: string; addon: string; type: string; name: string; config: Record<string, unknown>; favorites: Fav[] };
type TestResult = { ok: boolean; detail?: string; error?: string } | null;

export function ConnectionsSection({ only, compact }: { only?: Capability[]; compact?: boolean }) {
  const { data, reload } = useConnections(5000);
  const { data: ad } = useAddons();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [adding, setAdding] = useState(false);
  const caps = only ?? CAPS;
  const conns = (data?.connections ?? []).filter((c) => c.capabilities.some((k) => caps.includes(k)) || (!c.capabilities.length && !only));
  const addons = (ad?.addons ?? []).filter((a) => a.info.enabled).map((a) => a.info.manifest);
  const typeOf = (c: { addon: string; type: string }) => addons.find((m) => m.id === c.addon)?.connectionTypes.find((t) => t.id === c.type);

  const edit = (c: Connection) => {
    setAdding(false);
    setDraft({ id: c.id, addon: c.addon, type: c.type, name: c.name, config: { ...c.config }, favorites: c.favorites ?? [] });
  };
  const pick = (m: Manifest, t: ConnectionType) => {
    setAdding(false);
    const n = conns.filter((c) => c.type === t.id && c.addon === m.id).length;
    setDraft({ addon: m.id, type: t.id, name: n ? `${t.label} ${n + 1}` : t.label, config: defaults(t), favorites: [] });
  };
  const panel = draft ? (
    <ConnectionForm key={draft.id ?? draft.type} draft={draft} type={typeOf(draft)} conn={conns.find((c) => c.id === draft.id)}
      onChange={setDraft} onDone={() => { setDraft(null); reload(); }} />
  ) : (
    <Catalog addons={addons} caps={caps} onPick={pick} />
  );

  return (
    <div className={cn("grid gap-4", !compact && "xl:grid-cols-[minmax(0,1fr)_400px]")}>
      <div className="min-w-0 space-y-3">
        {!compact && (
          <div className="flex items-start gap-3">
            <div className="min-w-0 flex-1">
              <h2 className="text-xl font-bold">Connections</h2>
              <p className="text-sm text-muted">The gear this controller talks to. Add one per thing you own — two Proclaim PCs is two connections.</p>
            </div>
            <Button variant="accent" className="xl:hidden" onClick={() => { setDraft(null); setAdding(true); }}><Plus size={16} /> Add</Button>
          </div>
        )}
        {caps.map((k) => {
          const list = conns.filter((c) => c.capabilities.includes(k));
          return (
            <section key={k}>
              <h3 className="mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">{CAP_LABEL[k]}</h3>
              {list.length === 0 ? (
                <p className="rounded-xl border border-dashed border-line px-3 py-2.5 text-xs text-dim">None yet.</p>
              ) : (
                <div className="space-y-2">{list.map((c) => <Row key={c.id} c={c} active={draft?.id === c.id} onEdit={() => edit(c)} />)}</div>
              )}
            </section>
          );
        })}
        {conns.some((c) => !c.capabilities.length) && (
          <section>
            <h3 className="mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">Add-on missing or off</h3>
            <div className="space-y-2">{conns.filter((c) => !c.capabilities.length).map((c) => <Row key={c.id} c={c} active={draft?.id === c.id} onEdit={() => edit(c)} />)}</div>
          </section>
        )}
        {compact && (
          <Button onClick={() => { setDraft(null); setAdding(true); }}><Plus size={16} /> Add connection</Button>
        )}
      </div>
      {/* Side panel on wide screens, a dialog on phones / in the wizard. */}
      {!compact && <aside className="hidden xl:block">{panel}</aside>}
      <Dialog open={(compact || window.innerWidth < 1280) && (adding || !!draft)} onOpenChange={(o) => { if (!o) { setAdding(false); setDraft(null); } }}
        title={draft ? (draft.id ? "Edit connection" : "New connection") : "Add connection"} wide>
        {draft ? panel : <Catalog addons={addons} caps={caps} onPick={pick} bare />}
      </Dialog>
    </div>
  );
}

function Row({ c, active, onEdit }: { c: Connection; active: boolean; onEdit: () => void }) {
  const dot = c.disabled ? "bg-dim" : c.ready ? "bg-good" : "bg-bad";
  return (
    <button type="button" onClick={onEdit}
      className={cn("flex w-full items-center gap-3 rounded-xl border bg-panel px-3.5 py-3 text-left transition hover:border-line-strong", active ? "border-accent" : "border-line")}>
      <span className={cn("h-2 w-2 shrink-0 rounded-full", dot)} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-semibold">{c.name}</span>
        <span className="block truncate text-xs text-muted">
          {c.typeLabel ?? c.type}{c.addonName && c.addonName !== c.typeLabel ? ` · ${c.addonName}` : ""} · {c.disabled ? "turned off" : c.ready ? "connected" : c.error || "not connected"}
        </span>
      </span>
      <span className="text-xs text-muted">Edit</span>
    </button>
  );
}

function Catalog({ addons, caps, onPick, bare }: { addons: Manifest[]; caps: Capability[]; onPick: (m: Manifest, t: ConnectionType) => void; bare?: boolean }) {
  const [q, setQ] = useState("");
  const types = useMemo(() => addons.flatMap((m) => m.connectionTypes.map((t) => ({ m, t }))), [addons]);
  const match = ({ m, t }: { m: Manifest; t: ConnectionType }) => !q || `${m.name} ${t.label} ${t.description}`.toLowerCase().includes(q.toLowerCase());
  const groups = [
    ...caps.map((k) => ({ title: CAP_LABEL[k], items: types.filter((x) => !x.m.generic && x.t.capabilities.includes(k) && match(x)) })),
    { title: "Generic — for gear with no add-on yet", items: types.filter((x) => x.m.generic && x.t.capabilities.some((k) => caps.includes(k)) && match(x)) },
  ];
  return (
    <div className={cn(!bare && "rounded-2xl border border-accent/60 bg-panel p-4")}>
      {!bare && <h3 className="text-base font-bold">Add connection</h3>}
      <p className="mb-3 text-xs text-muted">What do you want to control? More kinds come with more add-ons (Settings → Add-ons).</p>
      <label className="mb-3 flex items-center gap-2 rounded-lg border border-line bg-bg px-2.5">
        <Search size={14} className="text-dim" />
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search: vmix, proclaim, wing…" className="h-9 flex-1 bg-transparent text-sm outline-none" />
      </label>
      <div className="space-y-3">
        {groups.filter((g) => g.items.length).map((g) => (
          <div key={g.title}>
            <h4 className="mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">{g.title}</h4>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-2">
              {g.items.map(({ m, t }) => (
                <button key={m.id + t.id} type="button" onClick={() => onPick(m, t)}
                  className="rounded-xl border border-line bg-panel-2 px-3 py-2 text-left transition hover:border-accent">
                  <span className="block text-sm font-semibold">{t.label}</span>
                  <span className="line-clamp-2 block text-[11px] text-muted">{t.description || m.description}</span>
                </button>
              ))}
            </div>
          </div>
        ))}
        {types.length === 0 && <p className="text-sm text-muted">No add-ons are installed or turned on. Settings → Add-ons.</p>}
      </div>
    </div>
  );
}

function ConnectionForm({ draft, type, conn, onChange, onDone }: {
  draft: Draft; type?: ConnectionType; conn?: Connection; onChange: (d: Draft) => void; onDone: () => void;
}) {
  const { log } = useLog();
  const [adv, setAdv] = useState(false);
  const [test, setTest] = useState<TestResult>(null);
  const [busy, setBusy] = useState<"" | "test" | "save" | "delete">("");
  const [confirm, setConfirm] = useState(false);
  const set = (k: string, v: unknown) => onChange({ ...draft, config: { ...draft.config, [k]: v } });
  const body = { addon: draft.addon, type: draft.type, name: draft.name, config: draft.config, favorites: draft.favorites, id: draft.id };
  const run = async (what: "test" | "save" | "delete") => {
    setBusy(what);
    try {
      if (what === "test") setTest(await api<TestResult>("/api/connections/test", "POST", body));
      if (what === "save") {
        await api(draft.id ? `/api/connections/${draft.id}` : "/api/connections", draft.id ? "PUT" : "POST", body);
        log("Settings", `${draft.name} saved`, true);
        onDone();
      }
      if (what === "delete" && draft.id) {
        await api(`/api/connections/${draft.id}`, "DELETE");
        log("Settings", `${draft.name} removed`, true);
        onDone();
      }
    } catch (e) {
      if (what === "test") setTest({ ok: false, error: (e as Error).message });
      else log("Settings", (e as Error).message, false);
    } finally {
      setBusy("");
    }
  };
  if (!type) {
    return (
      <div className="rounded-2xl border border-line bg-panel p-4 text-sm">
        <p className="font-semibold">{draft.name}</p>
        <p className="mt-1 text-muted">Its add-on ({draft.addon}) isn't installed or is turned off, so it can't be edited. Settings → Add-ons.</p>
        <div className="mt-3 flex gap-2">
          {draft.id && <Button variant="ghost" className="text-bad" onClick={() => setConfirm(true)}><Trash2 size={15} /> Remove</Button>}
          <Button variant="ghost" className="ml-auto" onClick={onDone}>Close</Button>
        </div>
        <Confirm open={confirm} name={draft.name} onClose={() => setConfirm(false)} onYes={() => run("delete")} />
      </div>
    );
  }
  const fields = type.fields.filter((f) => adv || !f.advanced);
  const hasAdv = type.fields.some((f) => f.advanced);
  return (
    <div className="space-y-3 rounded-2xl border border-accent/60 bg-panel p-4">
      <div className="flex items-center gap-2">
        <h3 className="flex-1 text-base font-bold">{draft.id ? "Edit connection" : `New ${type.label} connection`}</h3>
        <Button size="icon" variant="ghost" onClick={onDone} aria-label="Close"><X size={16} /></Button>
      </div>
      {type.description && <p className="text-xs text-muted">{type.description}</p>}
      <label className="block">
        <span className="mb-1 block text-xs font-medium text-muted">Name</span>
        <Input value={draft.name} maxLength={60} onChange={(e) => onChange({ ...draft, name: e.target.value })} placeholder={`${type.label} — office PC`} />
      </label>
      {fields.map((f) => (
        <FieldRow key={f.key} f={f} value={draft.config[f.key]} onChange={(v) => set(f.key, v)} addon={draft.addon} hasSecret={!!draft.config[`_has_${f.key}`]} />
      ))}
      {hasAdv && (
        <button type="button" className="text-xs text-muted underline-offset-2 hover:text-fg hover:underline" onClick={() => setAdv(!adv)}>
          {adv ? "Hide advanced" : "Advanced settings"}
        </button>
      )}
      {type.capabilities.includes("mixer") && <QuickChannels favs={draft.favorites} onChange={(favorites) => onChange({ ...draft, favorites })} />}
      {conn && !conn.ready && conn.error && !test && <p className="text-xs text-bad">Now: {conn.error}</p>}
      {test && (
        <p className={cn("flex items-start gap-1.5 text-sm", test.ok ? "text-good" : "text-bad")}>
          {test.ok ? <CheckCircle2 size={16} className="mt-0.5 shrink-0" /> : <XCircle size={16} className="mt-0.5 shrink-0" />}
          {test.ok ? test.detail : test.error}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2 pt-1">
        <Button onClick={() => run("test")} disabled={!!busy}>{busy === "test" ? <Loader2 size={15} className="animate-spin" /> : null} Test</Button>
        <Button variant="accent" onClick={() => run("save")} disabled={!!busy}>{busy === "save" ? <Loader2 size={15} className="animate-spin" /> : <Save size={15} />} Save</Button>
        {draft.id && <Button variant="ghost" className="ml-auto text-bad" onClick={() => setConfirm(true)} disabled={!!busy}><Trash2 size={15} /> Remove</Button>}
      </div>
      <Confirm open={confirm} name={draft.name} onClose={() => setConfirm(false)} onYes={() => { setConfirm(false); run("delete"); }} />
    </div>
  );
}

function Confirm({ open, name, onClose, onYes }: { open: boolean; name: string; onClose: () => void; onYes: () => void }) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title={`Remove “${name}”?`}
      description="Widgets and macros that use it will say it's missing until you pick another connection.">
      <div className="flex justify-end gap-2">
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
        <Button variant="danger" onClick={onYes}><Trash2 size={15} /> Remove</Button>
      </div>
    </Dialog>
  );
}
