import { useState } from "react";
import { Play, Plus, Pencil, Trash2, Upload, ChevronUp, ChevronDown, X, CheckCircle2, XCircle, Loader2 } from "lucide-react";
import { api, type Macro, type Step, type RunReport, type MixerStatus, type Preset } from "@/lib/api";
import { usePoll } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { Button, Card, CardBody, CardHeader, Dialog, Empty, Input, Select, Switch } from "@/components/ui";

export function describe(s: Step): string {
  switch (s.type) {
    case "delay": return (s.ms ?? 0) >= 1000 ? `Wait ${(s.ms ?? 0) / 1000} s` : `Wait ${s.ms} ms`;
    case "mixer_mute": return `${s.muted ? "Mute" : "Unmute"} ${s.channelName ? `${s.channelName} · ` : ""}Ch ${s.channel}`;
    case "switcher": return s.action === "preview" ? `Switcher: preview ${s.input}` : s.action === "fade" ? `Switcher: fade ${s.durationMs ?? 1000} ms` : "Switcher: cut";
    case "slides": return `Slides: ${s.action}`;
    case "camera_preset": return `Camera: ${s.preset}`;
  }
}

export function Macros() {
  const { data, reload } = usePoll<Macro[]>("/api/macros", 10000);
  const [running, setRunning] = useState<number | null>(null);
  const [report, setReport] = useState<RunReport | null>(null);
  const [editing, setEditing] = useState<Macro | "new" | null>(null);
  const [importing, setImporting] = useState(false);
  const [confirmDel, setConfirmDel] = useState<Macro | null>(null);
  const { log } = useLog();

  const run = async (m: Macro) => {
    setRunning(m.id);
    try {
      const r = await api<RunReport>(`/api/macros/${m.id}/run`, "POST");
      setReport(r);
      log("Macro", r.ok ? `“${m.name}” ran — ${r.steps.length} steps verified` : `“${m.name}” stopped — ${r.error}`, r.ok);
    } catch (e) {
      log("Macro", `“${m.name}” — ${(e as Error).message}`, false);
    } finally {
      setRunning(null);
      reload();
    }
  };
  const del = async () => {
    if (!confirmDel) return;
    await api(`/api/macros/${confirmDel.id}`, "DELETE").catch((e) => log("Macro", (e as Error).message, false));
    setConfirmDel(null);
    reload();
  };

  return (
    <Card>
      <CardHeader title="Macros" meta="chained actions">
        <Button size="sm" variant="ghost" onClick={() => setImporting(true)}><Upload size={14} /> Import</Button>
        <Button size="sm" onClick={() => setEditing("new")}><Plus size={14} /> New</Button>
      </CardHeader>
      <CardBody className="space-y-2">
        {(data ?? []).length === 0 ? <Empty>No macros yet. A macro runs its steps in order and stops at the first one that doesn't land.</Empty> :
          data!.map((m) => (
            <div key={m.id} className="flex items-center gap-2 rounded-xl border border-line bg-panel-2 p-2">
              <Button size="lg" variant="accent" className="w-14 shrink-0 px-0" onClick={() => run(m)} disabled={running !== null} aria-label={`Run ${m.name}`}>
                {running === m.id ? <Loader2 size={18} className="animate-spin" /> : <Play size={18} fill="currentColor" />}
              </Button>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-semibold">{m.name}</div>
                <div className="truncate text-xs text-dim">{m.steps.map(describe).join(" → ")}</div>
              </div>
              {m.lastRun && (m.lastRun.ok ? <CheckCircle2 size={16} className="shrink-0 text-good" /> : <XCircle size={16} className="shrink-0 text-bad" />)}
              <Button size="icon" variant="ghost" onClick={() => setEditing(m)} aria-label="Edit"><Pencil size={15} /></Button>
              <Button size="icon" variant="ghost" className="text-bad" onClick={() => setConfirmDel(m)} aria-label="Delete"><Trash2 size={15} /></Button>
            </div>
          ))}
      </CardBody>

      <Dialog open={!!report} onOpenChange={(o) => !o && setReport(null)} title={report ? `${report.name} — ${report.ok ? "done" : "stopped"}` : ""}
        description={report?.ok ? "Every step landed." : report?.error ?? undefined}>
        <ol className="space-y-1.5">
          {report?.steps.map((s) => (
            <li key={s.step} className="flex items-start gap-2 text-sm">
              {s.ok ? <CheckCircle2 size={16} className="mt-0.5 shrink-0 text-good" /> : <XCircle size={16} className="mt-0.5 shrink-0 text-bad" />}
              <div><div>{s.label}</div><div className="text-xs text-dim">{s.ok ? s.detail : s.error} · {s.ms} ms</div></div>
            </li>
          ))}
        </ol>
      </Dialog>
      {editing && <MacroEditor macro={editing === "new" ? null : editing} onClose={() => { setEditing(null); reload(); }} />}
      {importing && <ImportDialog onClose={() => { setImporting(false); reload(); }} />}
      <Dialog open={!!confirmDel} onOpenChange={(o) => !o && setConfirmDel(null)} title={`Delete “${confirmDel?.name}”?`} description="This can't be undone.">
        <div className="flex justify-end gap-2"><Button variant="ghost" onClick={() => setConfirmDel(null)}>Cancel</Button><Button variant="danger" onClick={del}>Delete</Button></div>
      </Dialog>
    </Card>
  );
}

const newStep = (type: Step["type"]): Step => {
  switch (type) {
    case "mixer_mute": return { type, channel: 1, muted: true };
    case "delay": return { type, ms: 500 };
    case "switcher": return { type, action: "cut" };
    case "slides": return { type, action: "next" };
    case "camera_preset": return { type, preset: "" };
  }
};

function MacroEditor({ macro, onClose }: { macro: Macro | null; onClose: () => void }) {
  const [name, setName] = useState(macro?.name ?? "");
  const [desc, setDesc] = useState(macro?.description ?? "");
  const [steps, setSteps] = useState<Step[]>(macro?.steps ?? []);
  const [err, setErr] = useState("");
  const mixer = usePoll<MixerStatus>("/api/mixer/status", 0).data;
  const presets = usePoll<Preset[]>("/api/presets", 0).data ?? [];
  const favs = (mixer?.channels ?? []).filter((c) => c.channel != null);

  const upd = (i: number, p: Partial<Step>) => setSteps((s) => s.map((x, j) => (j === i ? { ...x, ...p } : x)));
  const mv = (i: number, d: number) => setSteps((s) => {
    const j = i + d;
    if (j < 0 || j >= s.length) return s;
    const n = [...s];
    [n[i], n[j]] = [n[j], n[i]];
    return n;
  });
  const save = async () => {
    setErr("");
    const clean = steps.map((s) => {
      const o: Record<string, unknown> = { type: s.type };
      if (s.type === "mixer_mute") { if (s.channelName) o.channelName = s.channelName; else o.channel = s.channel; o.muted = s.muted; }
      if (s.type === "delay") o.ms = s.ms;
      if (s.type === "switcher") { o.action = s.action; if (s.action === "preview") o.input = s.input; if (s.action === "fade") o.durationMs = s.durationMs ?? 1000; }
      if (s.type === "slides") o.action = s.action;
      if (s.type === "camera_preset") o.preset = s.preset;
      return o;
    });
    try {
      const doc = { name, description: desc, steps: clean };
      if (macro) await api(`/api/macros/${macro.id}`, "PATCH", doc);
      else await api("/api/macros", "POST", doc);
      onClose();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={macro ? "Edit macro" : "New macro"} wide
      description="Steps run top to bottom. Mixer steps are read back from the console; the macro stops at the first step that doesn't land.">
      <div className="space-y-3">
        <div className="grid gap-2 sm:grid-cols-2">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Name, e.g. PC ⇄ Mic: Mic live" maxLength={60} />
          <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="Description (optional)" maxLength={300} />
        </div>
        <ol className="space-y-2">
          {steps.map((s, i) => (
            <li key={i} className="rounded-xl border border-line bg-panel-2 p-2.5">
              <div className="mb-2 flex items-center gap-1">
                <span className="font-mono text-xs text-dim">{i + 1}</span>
                <span className="ml-1 flex-1 text-sm font-semibold">{describe(s)}</span>
                <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => mv(i, -1)} aria-label="Up"><ChevronUp size={15} /></Button>
                <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => mv(i, 1)} aria-label="Down"><ChevronDown size={15} /></Button>
                <Button size="icon" variant="ghost" className="h-8 w-8 text-bad" onClick={() => setSteps(steps.filter((_, j) => j !== i))} aria-label="Remove"><X size={15} /></Button>
              </div>
              <div className="grid gap-2 sm:grid-cols-3">
                {s.type === "mixer_mute" && <>
                  <Select value={s.channelName ?? `#${s.channel}`} onChange={(v) => v.startsWith("#") ? upd(i, { channel: Number(v.slice(1)), channelName: undefined }) : upd(i, { channelName: v, channel: favs.find((f) => f.label === v)?.channel ?? undefined })}
                    options={[...favs.map((f) => ({ value: f.label, label: `${f.label} (Ch ${f.channel})` })), ...Array.from({ length: 48 }, (_, k) => ({ value: `#${k + 1}`, label: `Channel ${k + 1}` }))]} />
                  <label className="flex items-center gap-2 text-sm"><Switch checked={!!s.muted} onChange={(v) => upd(i, { muted: v })} /> {s.muted ? "Mute" : "Unmute"}</label>
                </>}
                {s.type === "delay" && <Input type="number" min={0} max={60000} value={s.ms ?? 0} onChange={(e) => upd(i, { ms: Number(e.target.value) })} />}
                {s.type === "switcher" && <>
                  <Select value={s.action ?? "cut"} onChange={(v) => upd(i, { action: v })} options={[{ value: "cut", label: "Cut" }, { value: "fade", label: "Fade" }, { value: "preview", label: "Preview input" }]} />
                  {s.action === "preview" && <Input type="number" min={1} value={s.input ?? 1} onChange={(e) => upd(i, { input: Number(e.target.value) })} />}
                  {s.action === "fade" && <Input type="number" min={0} max={10000} value={s.durationMs ?? 1000} onChange={(e) => upd(i, { durationMs: Number(e.target.value) })} />}
                </>}
                {s.type === "slides" && <Select value={s.action ?? "next"} onChange={(v) => upd(i, { action: v })} options={["next", "prev", "black", "logo", "clear"].map((a) => ({ value: a, label: a }))} />}
                {s.type === "camera_preset" && <Select value={s.preset ?? ""} onChange={(v) => upd(i, { preset: v })} options={[{ value: "", label: "Choose preset…" }, ...presets.map((p) => ({ value: p.name, label: p.name }))]} />}
              </div>
            </li>
          ))}
        </ol>
        <div className="flex flex-wrap gap-2">
          {(["mixer_mute", "delay", "switcher", "slides", "camera_preset"] as const).map((t) => (
            <Button key={t} size="sm" variant="outline" onClick={() => setSteps([...steps, newStep(t)])}>
              <Plus size={13} /> {{ mixer_mute: "Mute", delay: "Wait", switcher: "Switcher", slides: "Slides", camera_preset: "Camera preset" }[t]}
            </Button>
          ))}
        </div>
        {err && <p className="text-sm text-bad">{err}</p>}
        <div className="flex justify-end gap-2 pt-1"><Button variant="ghost" onClick={onClose}>Cancel</Button><Button variant="accent" onClick={save}>Save macro</Button></div>
      </div>
    </Dialog>
  );
}

function ImportDialog({ onClose }: { onClose: () => void }) {
  const [text, setText] = useState("");
  const [result, setResult] = useState<{ ok: boolean; msg: string; errors?: { index: number; name?: string; error: string }[] } | null>(null);
  const send = async (validateOnly: boolean) => {
    let body: unknown;
    try {
      body = JSON.parse(text);
    } catch {
      return setResult({ ok: false, msg: "That isn't valid JSON." });
    }
    const r = await fetch(`/api/macros/import${validateOnly ? "?validateOnly=true" : ""}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    const d = await r.json().catch(() => ({}));
    if (!r.ok) return setResult({ ok: false, msg: d.detail ?? `HTTP ${r.status}`, errors: d.errors });
    if (validateOnly) return setResult({ ok: true, msg: `Valid: ${d.macros.length} macro(s) ready to import.` });
    onClose();
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title="Import macros" wide description="One macro, a list, or {&quot;macros&quot;: [...]}. All-or-nothing: if any fails, nothing is imported. See MACROS.md.">
      <textarea value={text} onChange={(e) => setText(e.target.value)} spellCheck={false}
        className="h-56 w-full rounded-xl border border-line bg-bg p-3 font-mono text-xs text-fg focus:border-accent focus:outline-none"
        placeholder='{"name": "Sermon start", "steps": [{"type": "camera_preset", "preset": "Main"}, {"type": "switcher", "action": "cut"}]}' />
      {result && (
        <div className={`mt-2 text-sm ${result.ok ? "text-good" : "text-bad"}`}>
          {result.msg}
          {result.errors?.map((e) => <div key={e.index} className="text-xs text-muted">#{e.index + 1} {e.name ? `“${e.name}”` : ""}: {e.error}</div>)}
        </div>
      )}
      <div className="mt-3 flex justify-end gap-2"><Button variant="ghost" onClick={() => send(true)}>Check</Button><Button variant="accent" onClick={() => send(false)}>Import</Button></div>
    </Dialog>
  );
}
