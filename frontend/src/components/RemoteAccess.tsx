import { useEffect, useState } from "react";
import { Check, Copy, Plus, QrCode, X } from "lucide-react";
import { api } from "@/lib/api";
import { useShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Badge, Button, Card, CardBody, CardHeader, Dialog, Input, Switch } from "@/components/ui";

// Settings → Access → Remote access: a link that works from anywhere
// (Cloudflare Tunnel), only for the people listed (Cloudflare Access signs
// them in). Each person sees everything or only their own dashboards. The
// local network is untouched.

type Person = { email: string; name: string; everything: boolean; dashboards: number[]; lastSeen?: string };
type Config = { enabled: boolean; port: number; link: string; team: string; aud: string; people: Person[] };
type Resp = { config: Config; lastRequest?: string };
type Dash = { id: number; name: string; kind: string };

const ago = (iso?: string) => {
  if (!iso) return "";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString([], { month: "short", day: "numeric" });
};

export function RemoteAccess() {
  const { log } = useLog();
  const [data, setData] = useState<Resp | null>(null);
  const [cfg, setCfg] = useState<Config | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [qr, setQr] = useState(false);
  const [adding, setAdding] = useState(false);
  const { data: dashes } = useShared<Dash[]>("/api/dashboards", 30000);

  const fix = (r: Resp): Resp => ({ ...r, config: { ...r.config, people: r.config.people ?? [] } });
  const load = () => api<Resp>("/api/remote").then((r) => { r = fix(r); setData(r); setCfg(r.config); setError(""); }).catch((e) => setError((e as Error).message));
  useEffect(() => { load(); const t = setInterval(load, 30000); return () => clearInterval(t); }, []);

  const save = async (next: Config, what: string) => {
    setBusy(true);
    try {
      const r = fix(await api<Resp>("/api/remote", "PUT", next));
      setData(r); setCfg(r.config); setError("");
      log("Remote access", what);
    } catch (e) {
      setError((e as Error).message);
      log("Remote access", (e as Error).message, false);
    } finally {
      setBusy(false);
    }
  };
  if (!cfg) return <Card><CardHeader title="Remote access" /><CardBody><p className="text-xs text-muted">{error || "Loading…"}</p></CardBody></Card>;

  const seen = data?.lastRequest && Date.now() - new Date(data.lastRequest).getTime() < 15 * 60_000;
  const status = !cfg.enabled ? <Badge>Off</Badge> : seen ? <Badge tone="good" dot>On · tunnel connected</Badge> : <Badge tone="warn" dot>On · waiting for the tunnel</Badge>;
  const dashName = (id: number) => dashes?.find((d) => d.id === id)?.name ?? `#${id}`;
  const setPerson = (i: number, p: Person) => save({ ...cfg, people: cfg.people.map((x, j) => (j === i ? p : x)) }, `${p.name || p.email}: ${p.everything ? "everything" : p.dashboards.map(dashName).join(", ")}`);
  const remove = (i: number) => save({ ...cfg, people: cfg.people.filter((_, j) => j !== i) }, `Removed ${cfg.people[i].name || cfg.people[i].email}`);

  return (
    <Card>
      <CardHeader title="Remote access" meta="a link that works from anywhere, only for the people you list">
        {status}
      </CardHeader>
      <CardBody className="space-y-3">
        {error && <p className="rounded-lg border border-pgm/50 bg-pgm/10 px-3 py-2 text-xs text-bad">{error}</p>}

        <div className="flex flex-wrap items-center gap-2 rounded-xl border border-line bg-bg p-3">
          <div className="min-w-0 flex-1">
            <div className="text-[10px] font-bold uppercase tracking-[0.1em] text-dim">The link</div>
            <div className="truncate font-mono text-[15px] font-bold">{cfg.link || "Not set yet"}</div>
          </div>
          {cfg.link && (
            <>
              <Button size="sm" onClick={() => { navigator.clipboard?.writeText(cfg.link).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); }).catch(() => {}); }}>
                {copied ? <Check size={14} /> : <Copy size={14} />} {copied ? "Copied" : "Copy"}
              </Button>
              <Button size="sm" onClick={() => setQr(true)}><QrCode size={14} /> QR code</Button>
            </>
          )}
        </div>

        <div className="text-[10px] font-bold uppercase tracking-[0.1em] text-dim">Who can open it</div>
        <div className="space-y-2">
          {cfg.people.length === 0 && <p className="text-xs text-muted">Nobody yet. Add yourself to test, and the people who need it.</p>}
          {cfg.people.map((p, i) => (
            <PersonRow key={p.email} p={p} dashes={dashes ?? []} busy={busy} onChange={(np) => setPerson(i, np)} onRemove={() => remove(i)} />
          ))}
          <button type="button" onClick={() => setAdding(true)}
            className="flex w-full items-center gap-2 rounded-xl border border-line px-3 py-2.5 text-left text-sm font-semibold text-accent hover:border-line-strong">
            <Plus size={15} /> Add a person (their email, and which dashboard they see)
          </button>
          <p className="text-xs text-muted">“Sees” is their own dashboards (pick the phone and the iPad one; the right one opens by itself) or Everything. Someone not on this list never gets past Cloudflare's sign-in, and jadeck checks the sign-in again on every request.</p>
        </div>

        <div className="rounded-xl border border-info/40 bg-info/10 px-3 py-2.5">
          <div className="text-[13px] font-bold text-info">On this network: unchanged</div>
          <p className="mt-0.5 text-xs text-muted">Phones, iPads and PCs on the venue or home network open jadeck as today, no sign-in. This list only guards the link above, which comes in on its own door (port {cfg.port} on this computer only).</p>
        </div>

        <details className="rounded-xl border border-line bg-panel-2 px-3 py-2">
          <summary className="cursor-pointer text-xs font-semibold text-muted">Cloudflare details</summary>
          <CloudflareFields cfg={cfg} busy={busy} onSave={(c) => save(c, "Cloudflare details saved")} />
        </details>

        <div className="flex flex-wrap items-center gap-3">
          <span className="min-w-0 flex-1 text-[11px] text-dim">Sign-in by email code or Google, set in Cloudflare Access{data?.lastRequest ? ` · last request ${ago(data.lastRequest)}` : ""}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted">{cfg.enabled ? "On" : "Off"}</span>
            <Switch checked={cfg.enabled} disabled={busy} label="Remote access" onChange={(v) => save({ ...cfg, enabled: v }, v ? "Turned on" : "Turned off")} />
          </div>
        </div>
      </CardBody>

      <Dialog open={qr} onOpenChange={setQr} title="Scan to open" description={cfg.link}>
        <img alt={`QR code for ${cfg.link}`} className="mx-auto w-64 rounded-lg bg-white p-2" src={`/api/system/qr?text=${encodeURIComponent(cfg.link)}`} />
      </Dialog>
      <AddPerson open={adding} onOpenChange={setAdding} dashes={dashes ?? []} taken={cfg.people.map((p) => p.email)}
        onAdd={(p) => { setAdding(false); save({ ...cfg, people: [...cfg.people, p] }, `Added ${p.name || p.email}`); }} />
    </Card>
  );
}

function Sees({ p, dashes, onChange, disabled }: { p: Person; dashes: Dash[]; onChange: (p: Person) => void; disabled?: boolean }) {
  const chip = (on: boolean) => cn("no-drag h-8 rounded-lg border px-2.5 text-xs font-semibold transition", on ? "btn-sel" : "border-line bg-bg text-muted hover:text-fg");
  return (
    <div className="flex flex-wrap gap-1.5">
      <button type="button" disabled={disabled} className={chip(p.everything)} onClick={() => onChange({ ...p, everything: true, dashboards: [] })}>Everything</button>
      {dashes.map((d) => {
        const on = !p.everything && p.dashboards.includes(d.id);
        return (
          <button key={d.id} type="button" disabled={disabled} className={chip(on)}
            onClick={() => {
              const set = p.everything ? [] : p.dashboards;
              const next = on ? set.filter((x) => x !== d.id) : [...set, d.id];
              onChange({ ...p, everything: false, dashboards: next });
            }}>
            {d.name}
          </button>
        );
      })}
    </div>
  );
}

function PersonRow({ p, dashes, busy, onChange, onRemove }: { p: Person; dashes: Dash[]; busy: boolean; onChange: (p: Person) => void; onRemove: () => void }) {
  const [open, setOpen] = useState(false);
  const label = p.everything ? "Everything" : p.dashboards.map((id) => dashes.find((d) => d.id === id)?.name ?? `#${id}`).join(" + ");
  return (
    <div className="rounded-xl border border-line bg-panel-2 p-3">
      <div className="flex items-center gap-3">
        <span className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-sm font-bold", p.everything ? "bg-accent/20 text-accent" : "bg-info/20 text-info")}>
          {(p.name || p.email).slice(0, 1).toUpperCase()}
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate text-[14px] font-bold">{p.name || p.email}</div>
          <div className="truncate text-xs text-muted">{p.email}{p.lastSeen ? ` · last opened ${ago(p.lastSeen)}` : " · not opened yet"}</div>
        </div>
        <button type="button" onClick={() => setOpen(!open)} className="no-drag max-w-[45%] shrink-0 truncate rounded-lg border border-line bg-bg px-2.5 py-1.5 text-xs font-bold hover:border-line-strong">
          Sees: {label || "nothing"} ▾
        </button>
        <button type="button" aria-label={`Remove ${p.name || p.email}`} onClick={onRemove} className="no-drag shrink-0 rounded-lg p-1.5 text-dim hover:text-bad"><X size={15} /></button>
      </div>
      {open && <div className="mt-2.5"><Sees p={p} dashes={dashes} disabled={busy} onChange={(np) => { if (np.everything || np.dashboards.length) onChange(np); }} /></div>}
    </div>
  );
}

function AddPerson({ open, onOpenChange, dashes, taken, onAdd }: { open: boolean; onOpenChange: (v: boolean) => void; dashes: Dash[]; taken: string[]; onAdd: (p: Person) => void }) {
  const [p, setP] = useState<Person>({ email: "", name: "", everything: false, dashboards: [] });
  useEffect(() => { if (open) setP({ email: "", name: "", everything: false, dashboards: [] }); }, [open]);
  const email = p.email.trim().toLowerCase();
  const bad = !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email) ? "Their email (the one they sign in to Cloudflare with)" : taken.includes(email) ? "Already on the list" : !p.everything && !p.dashboards.length ? "Pick what they see" : "";
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Add a person" description="They sign in with this email (a code by email, or Google). Add the same email to the Cloudflare Access policy.">
      <div className="space-y-3">
        <label className="block space-y-1"><span className="text-xs font-semibold text-muted">Name</span>
          <Input value={p.name} onChange={(e) => setP({ ...p, name: e.target.value })} placeholder="Shown on their messages" /></label>
        <label className="block space-y-1"><span className="text-xs font-semibold text-muted">Email</span>
          <Input type="email" value={p.email} onChange={(e) => setP({ ...p, email: e.target.value })} placeholder="name@example.com" autoCapitalize="off" /></label>
        <div className="space-y-1"><span className="text-xs font-semibold text-muted">Sees</span><Sees p={p} dashes={dashes} onChange={setP} /></div>
        <div className="flex items-center justify-end gap-2">
          {bad && <span className="mr-auto text-xs text-dim">{bad}</span>}
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="accent" disabled={!!bad} onClick={() => onAdd({ ...p, email })}>Add</Button>
        </div>
      </div>
    </Dialog>
  );
}

function CloudflareFields({ cfg, busy, onSave }: { cfg: Config; busy: boolean; onSave: (c: Config) => void }) {
  const [c, setC] = useState(cfg);
  useEffect(() => setC(cfg), [cfg]);
  const changed = c.link !== cfg.link || c.team !== cfg.team || c.aud !== cfg.aud || c.port !== cfg.port;
  return (
    <div className="mt-2 space-y-2">
      <p className="text-xs text-muted">The tunnel (cloudflared) on this computer points your address at http://127.0.0.1:{c.port}. Cloudflare Access protects that address and signs people in; jadeck checks each sign-in with your team's keys and the application's audience tag.</p>
      <label className="block space-y-1"><span className="text-xs font-semibold text-muted">Link people open</span>
        <Input value={c.link} onChange={(e) => setC({ ...c, link: e.target.value })} placeholder="https://live.example.com" /></label>
      <div className="grid gap-2 sm:grid-cols-2">
        <label className="block space-y-1"><span className="text-xs font-semibold text-muted">Team name</span>
          <Input value={c.team} onChange={(e) => setC({ ...c, team: e.target.value })} placeholder="yourteam (yourteam.cloudflareaccess.com)" /></label>
        <label className="block space-y-1"><span className="text-xs font-semibold text-muted">Port on this computer</span>
          <Input type="number" value={c.port} onChange={(e) => setC({ ...c, port: Number(e.target.value) || 0 })} /></label>
      </div>
      <label className="block space-y-1"><span className="text-xs font-semibold text-muted">Application audience (AUD) tag</span>
        <Input value={c.aud} onChange={(e) => setC({ ...c, aud: e.target.value })} placeholder="Zero Trust → Access → Applications → your app → Overview" className="font-mono text-xs" /></label>
      <div className="flex justify-end"><Button size="sm" variant="accent" disabled={!changed || busy} onClick={() => onSave(c)}>Save</Button></div>
    </div>
  );
}
