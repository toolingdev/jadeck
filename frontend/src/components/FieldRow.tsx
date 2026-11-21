import { useEffect, useMemo, useState } from "react";
import { Check, ChevronDown, ChevronUp, GripVertical, Plus, X } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import type { Fav, Field, Opt } from "@/lib/addons";
import { Button, Input, Select, Switch } from "@/components/ui";

// Form pieces for add-on connection settings (drawn from each connection
// type's field list) and the mixer quick-channel editor.

export function FieldRow({ f, value, onChange, addon, hasSecret, conn, widget }: {
  f: Field; value: unknown; onChange: (v: unknown) => void; addon?: string; hasSecret?: boolean;
  /** A widget field (v2.1): live options are asked of this connection. */
  conn?: string; widget?: boolean;
}) {
  const [opts, setOpts] = useState<Opt[]>(f.options ?? []);
  useEffect(() => {
    if (!f.optionsFrom || !addon) return;
    const q = conn ? `?conn=${encodeURIComponent(conn)}` : "";
    let live = true;
    api<Opt[]>(`/api/connections/options/${encodeURIComponent(addon)}/${encodeURIComponent(f.optionsFrom)}${q}`)
      .then((o) => live && setOpts(o?.length ? o : f.options ?? [])).catch(() => {});
    return () => { live = false; };
  }, [f.optionsFrom, f.options, addon, conn]);
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
    case "order":
      return <div>{label}<OrderPick value={strList(value)} options={opts} onChange={onChange} label={f.label} />{help}</div>;
    case "select":
      if (f.multiple) return <div>{label}<MultiPick value={strList(value)} options={opts} onChange={onChange} />{help}</div>;
      if (f.optionsFrom && widget) {
        // A widget's live list (channel names...): pick one of them.
        const v = typeof value === "string" ? value : "";
        const list = [{ value: "", label: opts.length ? "Choose…" : "Nothing found yet" }, ...withMissing(opts, v ? [v] : [])];
        return <label className="block">{label}<Select value={v} onChange={onChange} options={list} />{help}</label>;
      }
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

// ------------------------------------------------- multi-select and order

const strList = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);

/** Options plus any saved value that's no longer offered (shown, so it can be removed). */
function withMissing(opts: Opt[], values: string[]): Opt[] {
  const known = new Set(opts.map((o) => o.value));
  return [...opts, ...values.filter((v) => !known.has(v)).map((v) => ({ value: v, label: `${v} (not found)` }))];
}

/** select + multiple: tap rows to pick several (value: string[] in option order). */
function MultiPick({ value, options, onChange }: { value: string[]; options: Opt[]; onChange: (v: string[]) => void }) {
  const all = withMissing(options, value);
  const on = new Set(value);
  const toggle = (v: string) => {
    const next = on.has(v) ? value.filter((x) => x !== v) : [...value, v];
    // keep the options' order, so the same picks always read the same
    const rank = new Map(all.map((o, i) => [o.value, i]));
    onChange(next.sort((a, b) => (rank.get(a) ?? 0) - (rank.get(b) ?? 0)));
  };
  if (!all.length) return <p className="rounded-lg border border-line bg-bg px-2.5 py-2 text-[11px] text-dim">Nothing to pick yet.</p>;
  return (
    <div className="max-h-60 space-y-0.5 overflow-y-auto rounded-lg border border-line bg-bg p-1" role="listbox" aria-multiselectable>
      {all.map((o) => (
        <button key={o.value} type="button" role="option" aria-selected={on.has(o.value)} onClick={() => toggle(o.value)}
          className={cn("flex min-h-8 w-full items-center gap-2 rounded-md px-2 text-left text-sm", on.has(o.value) ? "bg-raise" : "hover:bg-panel-2")}>
          <span className={cn("flex h-4 w-4 shrink-0 items-center justify-center rounded border", on.has(o.value) ? "border-accent bg-accent text-bg" : "border-line-strong")}>
            {on.has(o.value) && <Check size={12} strokeWidth={3} />}
          </span>
          <span className="min-w-0 flex-1 truncate">{o.label}</span>
        </button>
      ))}
    </div>
  );
}

/** order: pick from the options, then put them in order (drag the grip,
 *  or the arrows). Value: string[] in the chosen order. Touch friendly:
 *  every target is at least 32px. */
function OrderPick({ value, options, onChange, label }: { value: string[]; options: Opt[]; onChange: (v: string[]) => void; label: string }) {
  const all = withMissing(options, value);
  const name = new Map(all.map((o) => [o.value, o.label]));
  const rest = options.filter((o) => !value.includes(o.value));
  const [drag, setDrag] = useState<{ from: number; to: number; dy: number; y0: number; h: number } | null>(null);

  const move = (from: number, to: number) => {
    if (to < 0 || to >= value.length || from === to) return;
    const n = [...value];
    const [x] = n.splice(from, 1);
    n.splice(to, 0, x);
    onChange(n);
  };
  const down = (i: number, e: React.PointerEvent<HTMLButtonElement>) => {
    const row = (e.currentTarget.closest("[data-row]") as HTMLElement | null)?.getBoundingClientRect();
    e.currentTarget.setPointerCapture(e.pointerId);
    setDrag({ from: i, to: i, dy: 0, y0: e.clientY, h: (row?.height ?? 36) + 4 });
  };
  const moveTo = (e: React.PointerEvent) => {
    if (!drag) return;
    const dy = e.clientY - drag.y0;
    const to = Math.max(0, Math.min(value.length - 1, drag.from + Math.round(dy / drag.h)));
    setDrag({ ...drag, dy, to });
  };
  const up = () => {
    if (drag) move(drag.from, drag.to);
    setDrag(null);
  };
  // Where each row sits while one is dragged: the dragged one follows the
  // finger, the others slide to make room.
  const shift = (i: number) => {
    if (!drag) return 0;
    if (i === drag.from) return drag.dy;
    if (drag.from < drag.to && i > drag.from && i <= drag.to) return -drag.h;
    if (drag.from > drag.to && i < drag.from && i >= drag.to) return drag.h;
    return 0;
  };

  return (
    <div className="space-y-1.5 rounded-lg border border-line bg-bg p-1.5">
      {value.length > 0 ? (
        <div className="space-y-1" role="list" aria-label={label}>
          {value.map((v, i) => (
            <div key={v} data-row role="listitem"
              className={cn("relative flex min-h-9 items-center gap-0.5 rounded-md border bg-panel-2 py-0.5 pr-0.5", drag?.from === i ? "z-10 border-accent shadow-[0_6px_18px_rgba(0,0,0,.6)]" : "border-line")}
              style={{ transform: `translateY(${shift(i)}px)`, transition: drag && drag.from !== i ? "transform 120ms" : undefined }}>
              <button type="button" aria-label={`Drag ${name.get(v) ?? v}`} title="Drag to reorder"
                onPointerDown={(e) => down(i, e)} onPointerMove={moveTo} onPointerUp={up} onPointerCancel={() => setDrag(null)}
                className="flex h-8 w-8 shrink-0 cursor-grab touch-none items-center justify-center text-dim active:cursor-grabbing">
                <GripVertical size={15} />
              </button>
              <span className="w-4 shrink-0 text-right font-mono text-[11px] text-dim">{i + 1}</span>
              {/* long names wrap to two lines rather than cut off ("Previous…") in the narrow panel */}
              <span title={name.get(v) ?? v} className={cn("line-clamp-2 min-w-0 flex-1 break-words px-1 text-[13px] leading-[16px]", !options.some((o) => o.value === v) && "text-dim")}>{name.get(v) ?? v}</span>
              <button type="button" className="flex h-8 w-8 items-center justify-center rounded text-muted hover:bg-raise disabled:opacity-30" disabled={i === 0} onClick={() => move(i, i - 1)} aria-label="Move up"><ChevronUp size={15} /></button>
              <button type="button" className="flex h-8 w-8 items-center justify-center rounded text-muted hover:bg-raise disabled:opacity-30" disabled={i === value.length - 1} onClick={() => move(i, i + 1)} aria-label="Move down"><ChevronDown size={15} /></button>
              <button type="button" className="flex h-8 w-8 items-center justify-center rounded text-muted hover:bg-raise hover:text-bad" onClick={() => onChange(value.filter((x) => x !== v))} aria-label={`Remove ${name.get(v) ?? v}`}><X size={14} /></button>
            </div>
          ))}
        </div>
      ) : <p className="px-1 py-1 text-[11px] text-dim">Nothing picked yet. Add from the list below.</p>}
      {rest.length > 0 && (rest.length <= 12 ? (
        <div className="flex flex-wrap gap-1 border-t border-line pt-1.5">
          {rest.map((o) => (
            <button key={o.value} type="button" onClick={() => onChange([...value, o.value])}
              className="flex h-8 max-w-full items-center gap-1 rounded-lg border border-line bg-panel px-2.5 text-xs text-muted hover:border-line-strong hover:text-fg">
              <Plus size={12} className="shrink-0" /><span className="truncate">{o.label}</span>
            </button>
          ))}
        </div>
      ) : (
        <Select value="" onChange={(v) => v && onChange([...value, v])} options={[{ value: "", label: "＋ Add…" }, ...rest]} className="h-9" />
      ))}
      {!options.length && !value.length && <p className="px-1 text-[11px] text-dim">No choices found yet (is the connection on?).</p>}
    </div>
  );
}
