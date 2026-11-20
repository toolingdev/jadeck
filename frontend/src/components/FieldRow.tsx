import { useEffect, useMemo, useState } from "react";
import { ChevronDown, ChevronUp, Plus, X } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import type { Fav, Field, Opt } from "@/lib/addons";
import { Button, Input, Select, Switch } from "@/components/ui";

// Form pieces for add-on connection settings (drawn from each connection
// type's field list) and the mixer quick-channel editor.

export function FieldRow({ f, value, onChange, addon, hasSecret }: { f: Field; value: unknown; onChange: (v: unknown) => void; addon?: string; hasSecret?: boolean }) {
  const [opts, setOpts] = useState<Opt[]>(f.options ?? []);
  useEffect(() => {
    if (!f.optionsFrom || !addon) return;
    api<Opt[]>(`/api/connections/options/${addon}/${f.optionsFrom}`).then((o) => setOpts(o ?? [])).catch(() => {});
  }, [f.optionsFrom, addon]);
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
    case "scopes":
      return <ScopesField value={Array.isArray(value) ? (value as string[]) : []} onChange={onChange} label={label} help={help} />;
    case "textarea":
      return (
        <label className="block">{label}
          <textarea value={(value as string) ?? ""} rows={3} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)}
            className="w-full rounded-xl border border-line bg-bg p-2 text-sm focus:border-accent focus:outline-none" />{help}
        </label>
      );
    default:
      return (
        <label className="block">{label}
          <Input type={f.type === "password" ? "password" : "text"} value={(value as string) ?? ""} autoComplete={f.type === "password" ? "new-password" : undefined}
            placeholder={f.type === "password" && hasSecret ? "•••••• saved — type to change" : f.placeholder} onChange={(e) => onChange(e.target.value)} />{help}
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

export function QuickChannels({ favs, onChange }: { favs: Fav[]; onChange: (f: Fav[]) => void }) {
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
        <span className="text-sm font-semibold">Quick channels on this mixer</span>
        <Button size="sm" variant="ghost" className="ml-auto" onClick={() => onChange([...favs, { label: "", channel: 0 }])} disabled={favs.length >= 12}><Plus size={14} /> Add</Button>
      </div>
      <p className="mb-2 text-[11px] text-dim">The mixer has many channels; pick the ones volunteers need. They show on the Mute buttons and the Favorites widget, with these names, in this order. Channel 0 = fill in later.</p>
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

/** Which controller permissions a Controller add-on's connection gets. */
function ScopesField({ value, onChange, label, help }: { value: string[]; onChange: (v: unknown) => void; label: React.ReactNode; help: React.ReactNode }) {
  const [scopes, setScopes] = useState<{ id: string; name: string; description: string }[]>([]);
  useEffect(() => {
    api<{ scopes: { id: string; name: string; description: string }[] }>("/api/keys").then((r) => setScopes(r.scopes ?? [])).catch(() => {});
  }, []);
  const never = new Set(["addons.admin", "update.apply"]);
  return (
    <div>{label}
      <div className="grid grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-1.5">
        {scopes.filter((s) => !never.has(s.id)).map((s) => (
          <label key={s.id} title={s.description} className="flex items-center gap-2 rounded-lg border border-line bg-bg px-2 py-1.5 text-xs">
            <input type="checkbox" checked={value.includes(s.id)} className="accent-[var(--color-accent)]"
              onChange={(e) => onChange(e.target.checked ? [...value, s.id] : value.filter((x) => x !== s.id))} />
            {s.name}
          </label>
        ))}
      </div>{help}
    </div>
  );
}
