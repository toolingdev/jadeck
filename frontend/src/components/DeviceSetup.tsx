import { useEffect, useMemo, useState } from "react";
import { MonitorPlay, Presentation, SlidersVertical, Plug, CheckCircle2, XCircle, Loader2, ChevronDown, ChevronUp, Plus, X, Save } from "lucide-react";
import { api } from "@/lib/api";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Button, Card, CardBody, CardHeader, Input, Select, Switch } from "@/components/ui";

type Opt = { value: string; label: string };
type Field = {
  key: string; label: string; type: "text" | "number" | "select" | "bool" | "notes" | "json" | "password";
  default?: unknown; placeholder?: string; help?: string; required?: boolean; advanced?: boolean;
  min?: number; max?: number; options?: Opt[]; optionsFrom?: string; keys?: string[];
};
type Meta = { kind: Kind; driver: string; label: string; description: string; fields: Field[] };
type Kind = "switcher" | "presentation" | "mixer";
type Cfg = Record<string, unknown> & { driver?: string; label?: string };
type Fav = { label: string; channel: number };
type Setup = { metas: Meta[]; config: { switcher?: Cfg | null; presentation?: Cfg | null; mixer?: Cfg | null; favorites?: Fav[] | null } };

const kinds: { kind: Kind; title: string; icon: typeof Plug; none: string }[] = [
  { kind: "switcher", title: "Video switcher", icon: MonitorPlay, none: "No switcher" },
  { kind: "presentation", title: "Slides", icon: Presentation, none: "No slide control" },
  { kind: "mixer", title: "Audio mixer", icon: SlidersVertical, none: "No mixer" },
];

function defaults(m: Meta): Cfg {
  const c: Cfg = { driver: m.driver };
  for (const f of m.fields) if (f.default !== undefined) c[f.key] = structuredClone(f.default);
  return c;
}

/** Settings → Devices: pick gear, fill the form, test, save. Applies live. */
export function DeviceSetup({ only, title = "Devices" }: { only?: Kind[]; title?: string } = {}) {
  const shown = kinds.filter((k) => !only || only.includes(k.kind));
  const [setup, setSetup] = useState<Setup | null>(null);
  const [cfg, setCfg] = useState<Record<Kind, Cfg>>({ switcher: {}, presentation: {}, mixer: {} });
  const [favs, setFavs] = useState<Fav[]>([]);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const { log } = useLog();

  const load = async () => {
    try {
      const s = await api<Setup>("/api/devices/setup");
      setSetup(s);
      setCfg({ switcher: s.config.switcher ?? {}, presentation: s.config.presentation ?? {}, mixer: s.config.mixer ?? {} });
      setFavs(s.config.favorites ?? []);
      setDirty(false);
      setErrors({});
    } catch (e) {
      setMsg({ ok: false, text: (e as Error).message });
    }
  };
  useEffect(() => {
    load();
  }, []);

  const update = (k: Kind, c: Cfg) => {
    setCfg((o) => ({ ...o, [k]: c }));
    setDirty(true);
    setMsg(null);
  };
  const save = async () => {
    setSaving(true);
    setErrors({});
    setMsg(null);
    try {
      const r = await fetch("/api/devices/setup", {
        method: "PUT", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...cfg, favorites: favs.filter((f) => f.label.trim()) }),
      });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        setMsg({ ok: false, text: d.detail ?? `HTTP ${r.status}` });
        if (d.errors) setErrors(d.errors);
        return;
      }
      setMsg({ ok: true, text: "Saved — the new settings are live." });
      log("Settings", "Device settings saved and applied");
      await load();
    } catch {
      setMsg({ ok: false, text: "the controller is not answering" });
    } finally {
      setSaving(false);
    }
  };

  if (!setup) return <Card><CardHeader title={title} /><CardBody><p className="text-sm text-muted">{msg?.text ?? "Loading…"}</p></CardBody></Card>;
  return (
    <Card>
      <CardHeader title={title} meta={only ? "optional" : "what this controller talks to"}>
        {dirty && <span className="text-xs text-warn">Unsaved changes</span>}
        <Button size="sm" variant="ghost" onClick={load} disabled={saving}>Revert</Button>
        <Button size="sm" variant="accent" onClick={save} disabled={!dirty || saving}>
          {saving ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />} Save & apply
        </Button>
      </CardHeader>
      <CardBody className="space-y-3">
        <p className="text-xs text-muted">Pick what you have, fill in the address, press <b>Test</b>, then <b>Save & apply</b>. Nothing needs a restart. The camera is set up separately.</p>
        {msg && <div className={cn("rounded-xl border px-3 py-2 text-sm", msg.ok ? "border-good/30 bg-good/10 text-good" : "border-pgm/30 bg-pgm/10 text-bad")}>{msg.text}</div>}
        <div className={cn("grid gap-3", shown.length === 3 ? "xl:grid-cols-3" : shown.length === 2 && "lg:grid-cols-2")}>
          {shown.map((k) => (
            <KindCard key={k.kind} {...k} metas={setup.metas.filter((m) => m.kind === k.kind)}
              cfg={cfg[k.kind]} onChange={(c) => update(k.kind, c)} error={errors[k.kind]} />
          ))}
        </div>
        {cfg.mixer?.driver && shown.some((k) => k.kind === "mixer") && <Favorites favs={favs} onChange={(f) => { setFavs(f); setDirty(true); }} />}
      </CardBody>
    </Card>
  );
}

function KindCard({ kind, title, icon: Icon, none, metas, cfg, onChange, error }: {
  kind: Kind; title: string; icon: typeof Plug; none: string; metas: Meta[]; cfg: Cfg; onChange: (c: Cfg) => void; error?: string;
}) {
  const meta = metas.find((m) => m.driver === cfg.driver);
  const [adv, setAdv] = useState(false);
  const [test, setTest] = useState<{ busy?: boolean; ok?: boolean; text?: string } | null>(null);
  const basic = meta?.fields.filter((f) => !f.advanced) ?? [];
  const advanced = meta?.fields.filter((f) => f.advanced) ?? [];

  const pick = (driver: string) => {
    setTest(null);
    if (!driver) return onChange({});
    const m = metas.find((x) => x.driver === driver)!;
    onChange({ ...defaults(m), label: cfg.driver === driver ? cfg.label : "" });
  };
  const runTest = async () => {
    setTest({ busy: true });
    try {
      const r = await api<{ ok: boolean; detail?: string; error?: string }>("/api/devices/test", "POST", { kind, config: cfg });
      setTest({ ok: r.ok, text: r.ok ? r.detail : r.error });
    } catch (e) {
      setTest({ ok: false, text: (e as Error).message });
    }
  };

  return (
    <div className={cn("rounded-xl border bg-panel-2 p-3", error ? "border-pgm/50" : "border-line")}>
      <div className="mb-3 flex items-center gap-2">
        <Icon size={18} className={cfg.driver ? "text-accent" : "text-dim"} />
        <span className="text-sm font-semibold">{title}</span>
      </div>
      <Select value={cfg.driver ?? ""} onChange={pick}
        options={[{ value: "", label: none }, ...metas.map((m) => ({ value: m.driver, label: m.label }))]} />
      {meta && (
        <div className="mt-3 space-y-3">
          <p className="text-xs text-muted">{meta.description}</p>
          <FieldRow f={{ key: "label", label: "Name on screen", type: "text", placeholder: meta.label }} value={cfg.label} onChange={(v) => onChange({ ...cfg, label: v as string })} />
          {basic.map((f) => <FieldRow key={f.key} f={f} value={cfg[f.key]} onChange={(v) => onChange({ ...cfg, [f.key]: v })} />)}
          {advanced.length > 0 && (
            <>
              <button type="button" onClick={() => setAdv(!adv)} className="flex items-center gap-1 text-xs text-muted hover:text-fg">
                {adv ? <ChevronUp size={14} /> : <ChevronDown size={14} />} Advanced
              </button>
              {adv && advanced.map((f) => <FieldRow key={f.key} f={f} value={cfg[f.key]} onChange={(v) => onChange({ ...cfg, [f.key]: v })} />)}
            </>
          )}
          <div className="flex items-center gap-2 pt-1">
            <Button size="sm" onClick={runTest} disabled={test?.busy}>{test?.busy ? <Loader2 size={14} className="animate-spin" /> : <Plug size={14} />} Test</Button>
            {test && !test.busy && (
              <span className={cn("flex min-w-0 items-center gap-1 text-xs", test.ok ? "text-good" : "text-bad")}>
                {test.ok ? <CheckCircle2 size={14} className="shrink-0" /> : <XCircle size={14} className="shrink-0" />}
                <span className="truncate" title={test.text}>{test.text}</span>
              </span>
            )}
          </div>
        </div>
      )}
      {error && <p className="mt-2 text-xs text-bad">{error}</p>}
    </div>
  );
}

function FieldRow({ f, value, onChange }: { f: Field; value: unknown; onChange: (v: unknown) => void }) {
  const [opts, setOpts] = useState<Opt[]>(f.options ?? []);
  useEffect(() => {
    if (!f.optionsFrom) return;
    api<Opt[]>(`/api/devices/options/${f.optionsFrom}`).then((o) => setOpts(o)).catch(() => {});
  }, [f.optionsFrom]);
  const label = (
    <span className="mb-1 block text-xs font-medium text-muted">{f.label}{f.required && <span className="text-accent"> *</span>}</span>
  );
  const help = f.help && <span className="mt-1 block text-[11px] text-dim">{f.help}</span>;
  switch (f.type) {
    case "bool":
      return <label className="flex items-center justify-between gap-3 text-sm"><span className="text-muted">{f.label}</span><Switch checked={!!value} onChange={onChange} /></label>;
    case "number":
      return (
        <label className="block">{label}
          <Input type="number" value={value === undefined || value === null ? "" : String(value)} min={f.min} max={f.max} placeholder={f.placeholder}
            onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))} />{help}
        </label>
      );
    case "select":
      if (f.optionsFrom) {
        const id = `opts-${f.key}`;
        return (
          <label className="block">{label}
            <Input list={id} value={(value as string) ?? ""} placeholder={f.placeholder ?? (opts[0]?.value || "")} onChange={(e) => onChange(e.target.value)} />
            <datalist id={id}>{opts.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}</datalist>
            <span className="mt-1 block text-[11px] text-dim">{opts.length ? `Found on this PC: ${opts.map((o) => o.label).join(", ")}` : "No outputs found on this PC yet."} {f.help}</span>
          </label>
        );
      }
      return <label className="block">{label}<Select value={(value as string) ?? ""} onChange={onChange} options={opts} />{help}</label>;
    case "notes": {
      const notes = (value as Record<string, number>) ?? {};
      return (
        <div>{label}
          <div className="grid grid-cols-5 gap-1.5">
            {(f.keys ?? Object.keys(notes)).map((k) => (
              <label key={k} className="block text-center">
                <span className="block text-[10px] uppercase tracking-[0.08em] text-dim">{k}</span>
                <Input type="number" min={0} max={127} className="px-1 text-center" value={notes[k] ?? ""}
                  onChange={(e) => onChange({ ...notes, [k]: Number(e.target.value) })} />
              </label>
            ))}
          </div>{help}
        </div>
      );
    }
    case "json":
      return <JsonField value={value} onChange={onChange} label={label} help={help} />;
    default:
      return (
        <label className="block">{label}
          <Input type={f.type === "password" ? "password" : "text"} value={(value as string) ?? ""} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} />{help}
        </label>
      );
  }
}

function JsonField({ value, onChange, label, help }: { value: unknown; onChange: (v: unknown) => void; label: React.ReactNode; help: React.ReactNode }) {
  const initial = useMemo(() => (value === undefined ? "" : JSON.stringify(value, null, 2)), []); // eslint-disable-line react-hooks/exhaustive-deps
  const [text, setText] = useState(initial);
  const [bad, setBad] = useState(false);
  return (
    <label className="block">{label}
      <textarea value={text} spellCheck={false} rows={5}
        onChange={(e) => {
          setText(e.target.value);
          if (!e.target.value.trim()) { setBad(false); return onChange(undefined); }
          try { onChange(JSON.parse(e.target.value)); setBad(false); } catch { setBad(true); }
        }}
        className={cn("w-full rounded-xl border bg-bg p-2 font-mono text-xs focus:outline-none", bad ? "border-pgm" : "border-line focus:border-accent")} />
      {bad ? <span className="text-[11px] text-bad">Not valid JSON yet.</span> : help}
    </label>
  );
}

function Favorites({ favs, onChange }: { favs: Fav[]; onChange: (f: Fav[]) => void }) {
  const set = (i: number, p: Partial<Fav>) => onChange(favs.map((f, j) => (j === i ? { ...f, ...p } : f)));
  const mv = (i: number, d: number) => {
    const j = i + d;
    if (j < 0 || j >= favs.length) return;
    const n = [...favs];
    [n[i], n[j]] = [n[j], n[i]];
    onChange(n);
  };
  return (
    <div className="rounded-xl border border-line bg-panel-2 p-3">
      <div className="mb-1 flex items-center gap-2">
        <span className="text-sm font-semibold">Favorite channels</span>
        <span className="text-xs text-dim">the quick mute buttons, in this order</span>
        <Button size="sm" variant="ghost" className="ml-auto" onClick={() => onChange([...favs, { label: "", channel: 0 }])} disabled={favs.length >= 12}><Plus size={14} /> Add</Button>
      </div>
      <p className="mb-2 text-[11px] text-dim">Channel 0 = a placeholder you'll fill in later.</p>
      <div className="grid gap-2 sm:grid-cols-2">
        {favs.map((f, i) => (
          <div key={i} className="flex items-center gap-1.5">
            <span className="w-5 text-right font-mono text-[11px] text-dim">{i + 1}</span>
            <Input value={f.label} placeholder="Name, e.g. Pulpit" maxLength={24} onChange={(e) => set(i, { label: e.target.value })} />
            <Input type="number" min={0} max={128} className="w-20" value={f.channel} onChange={(e) => set(i, { channel: Number(e.target.value) })} aria-label="Channel" />
            <Button size="icon" variant="ghost" className="h-9 w-8" onClick={() => mv(i, -1)} aria-label="Up"><ChevronUp size={14} /></Button>
            <Button size="icon" variant="ghost" className="h-9 w-8" onClick={() => mv(i, 1)} aria-label="Down"><ChevronDown size={14} /></Button>
            <Button size="icon" variant="ghost" className="h-9 w-8 text-bad" onClick={() => onChange(favs.filter((_, j) => j !== i))} aria-label="Remove"><X size={14} /></Button>
          </div>
        ))}
      </div>
    </div>
  );
}
