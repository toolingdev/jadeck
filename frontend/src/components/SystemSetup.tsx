import { useEffect, useState } from "react";
import { Copy, Check, Download, Loader2, QrCode, RefreshCw, Plus, X, ChevronDown, ChevronUp } from "lucide-react";
import { api, type SystemInfo, type SystemSettings, type VideoInfo } from "@/lib/api";
import { usePoll } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Badge, Button, Card, CardBody, CardHeader, Dialog, Input, Segmented, Switch } from "@/components/ui";

function CopyText({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <span className="inline-flex min-w-0 max-w-full items-center gap-1 rounded-lg bg-bg px-2 py-1">
      <code className="min-w-0 truncate font-mono text-[11px]">{text}</code>
      <button type="button" aria-label="Copy" className="shrink-0 text-muted hover:text-fg" onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1400);
        } catch { /* select manually */ }
      }}>{done ? <Check size={13} /> : <Copy size={13} />}</button>
    </span>
  );
}

function Row({ title, sub, children }: { title: string; sub?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-xl border border-line bg-panel-2 px-3 py-2.5">
      <div className="min-w-0 flex-1">
        <div className="text-sm font-semibold">{title}</div>
        {sub && <div className="mt-0.5 text-xs text-muted">{sub}</div>}
      </div>
      {children}
    </div>
  );
}

const stateLabel: Record<string, { text: string; tone: "good" | "bad" | "warn" | "neutral" }> = {
  running: { text: "Running", tone: "good" },
  starting: { text: "Starting…", tone: "warn" },
  installing: { text: "Downloading…", tone: "warn" },
  missing: { text: "Not installed yet", tone: "warn" },
  failed: { text: "Stopped", tone: "bad" },
  off: { text: "Off", tone: "neutral" },
};

/** Settings → Video: the app runs the stream gateway itself. */
export function VideoCard() {
  const { data: v, reload, setData } = usePoll<VideoInfo>("/api/video", 4000);
  const [busy, setBusy] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [showLog, setShowLog] = useState(false);
  const { log } = useLog();
  if (!v) return <Card><CardHeader title="Stream gateway" /><CardBody><p className="text-sm text-muted">Loading…</p></CardBody></Card>;

  const save = async (patch: Partial<VideoInfo["settings"]>, cameras?: Record<string, boolean>) => {
    try {
      setData(await api<VideoInfo>("/api/video", "PUT", { ...v.settings, ...patch, cameras }));
    } catch (e) {
      log("Video", (e as Error).message, false);
    }
  };
  const install = async () => {
    setBusy(true);
    try {
      setData(await api<VideoInfo>("/api/video/install", "POST"));
      log("Video", "Stream gateway installed");
    } catch (e) {
      log("Video", (e as Error).message, false);
    }
    setBusy(false);
    reload();
  };
  const st = v.status;
  const label = stateLabel[st.state] ?? stateLabel.off;
  const managed = v.settings.managed;
  const ready = (name: string) => st.paths?.find((p) => p.name === name);

  return (
    <Card>
      <CardHeader title="Stream gateway" meta="camera video on or off · where video is relayed">
        {managed && <Badge tone={label.tone} dot>{label.text}</Badge>}
      </CardHeader>
      <CardBody className="space-y-2">
        {managed && st.state === "missing" && (
          <div className="flex flex-wrap items-center gap-3 rounded-xl border border-warn/30 bg-warn/5 p-3 text-sm">
            <div className="min-w-0 flex-1">
              <div className="font-semibold">One download turns on live video</div>
              <div className="text-xs text-muted">MediaMTX {st.version} (about 30 MB, checked against a fingerprint built into this app). Without it, phones see a still picture that refreshes every second.</div>
            </div>
            {st.canFetch ? (
              <Button variant="accent" onClick={install} disabled={busy}>{busy ? <Loader2 size={15} className="animate-spin" /> : <Download size={15} />} Download & start</Button>
            ) : <span className="text-xs text-muted">Put mediamtx beside the ptz program on this PC.</span>}
          </div>
        )}
        {managed && st.state === "failed" && st.error && <p className="rounded-xl border border-bad/30 bg-bad/5 p-3 text-xs text-bad">{st.error} — retrying.</p>}

        {v.cameras.length === 0 && <p className="text-sm text-muted">Add a camera first (Cameras, above).</p>}
        {v.cameras.map((c) => {
          const hd = ready(c.hd), sd = ready(c.sd);
          const watching = (hd?.readers ?? 0) + (sd?.readers ?? 0);
          return (
            <Row key={c.id} title={c.name} sub={c.video
              ? <>Phones get SD (low delay), desktops get HD · from the camera's RTSP{st.state === "running" && <> · {hd?.ready || sd?.ready ? <span className="text-good">streaming to {watching} {watching === 1 ? "screen" : "screens"}</span> : "starts when someone watches"}</>}</>
              : "Still pictures only"}>
              <Switch checked={c.video} onChange={(on) => save({}, { [c.id]: on })} label={`${c.name} live video`} />
            </Row>
          );
        })}
        <div className="flex flex-wrap gap-x-4 gap-y-1 pt-1 text-xs text-muted">
          <button type="button" className="flex items-center gap-1 hover:text-fg" onClick={() => setAdvanced((a) => !a)}>
            {advanced ? <ChevronUp size={13} /> : <ChevronDown size={13} />} Advanced: use my own MediaMTX
          </button>
          {managed && st.bundled && <span className="text-dim">Built in: MediaMTX {st.version} (MIT license)</span>}
          {managed && (st.log?.length ?? 0) > 0 && <button type="button" className="hover:text-fg" onClick={() => setShowLog(true)}>Gateway log</button>}
        </div>
        {advanced && (
          <div className="space-y-2 rounded-xl border border-line p-3">
            <label className="flex items-center gap-3 text-sm">
              <span className="flex-1">Run the stream gateway inside this app <span className="block text-xs text-muted">Turn off only if you already run MediaMTX yourself.</span></span>
              <Switch checked={managed} onChange={(on) => save({ managed: on })} label="Built-in gateway" />
            </label>
            {!managed && (
              <ExternalHost value={v.settings.external} onSave={(external) => save({ external })} />
            )}
          </div>
        )}
      </CardBody>
      <Dialog open={showLog} onOpenChange={setShowLog} title="Stream gateway log" wide>
        <pre className="max-h-[60dvh] overflow-auto rounded-xl bg-bg p-3 font-mono text-[11px] text-muted">{(st.log ?? []).join("\n")}</pre>
      </Dialog>
    </Card>
  );
}

function ExternalHost({ value, onSave }: { value: string; onSave: (v: string) => void }) {
  const [v, setV] = useState(value);
  return (
    <div className="flex gap-2">
      <Input value={v} onChange={(e) => setV(e.target.value)} placeholder="IP of the PC running MediaMTX, e.g. 10.1.10.20" />
      <Button onClick={() => onSave(v.trim())} disabled={v.trim() === value}>Save</Button>
    </div>
  );
}

/** Settings → System: network, who needs a key, updates. */
export function SystemCard() {
  const { data, reload } = usePoll<SystemInfo>("/api/system", 15000);
  const [f, setF] = useState<SystemSettings | null>(null);
  const [token, setToken] = useState("");
  const [clearToken, setClearToken] = useState(false);
  const [newNet, setNewNet] = useState("");
  const [saving, setSaving] = useState(false);
  const [checking, setChecking] = useState(false);
  const [err, setErr] = useState("");
  const [qr, setQr] = useState<string | null>(null);
  const [advanced, setAdvanced] = useState(false);
  const [moved, setMoved] = useState<string | null>(null);
  const { log } = useLog();
  useEffect(() => {
    if (data && !f) setF({ ...data.system, trustedCidrs: data.system.trustedCidrs ?? [] });
  }, [data, f]);
  if (!data || !f) return <Card><CardHeader title="System" /><CardBody><p className="text-sm text-muted">Loading…</p></CardBody></Card>;

  const orig = data.system;
  const dirty = JSON.stringify({ ...orig, trustedCidrs: orig.trustedCidrs ?? [] }) !== JSON.stringify(f) || token !== "" || clearToken;
  const set = (p: Partial<SystemSettings>) => {
    setF({ ...f, ...p });
    setErr("");
  };
  const save = async () => {
    if (f.port !== orig.port && !confirm(`Move the controller to port ${f.port}? Open screens reconnect at the new address.`)) return;
    setSaving(true);
    try {
      const r = await api<SystemInfo>("/api/system", "PUT", { ...f, updateToken: token, clearToken });
      setF({ ...r.system, trustedCidrs: r.system.trustedCidrs ?? [] });
      setToken("");
      setClearToken(false);
      log("System", "Saved");
      if (f.port !== orig.port) {
        const url = `${location.protocol}//${location.hostname}:${f.port}${location.pathname}`;
        setMoved(url);
        setTimeout(() => (location.href = url), 2500);
      }
      reload();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setSaving(false);
    }
  };
  const check = async () => {
    setChecking(true);
    await api("/api/update/check", "POST").catch(() => {});
    setChecking(false);
    reload();
  };
  const nets = f.trustedCidrs ?? [];
  const up = data.update;
  const upText: Record<string, string> = {
    none: "up to date", checking: "checking…", available: `update ${up.version} ready`, invalid: "a release failed verification",
    applying: "updating…", applied: `updated to ${up.version}`, rolled_back: `rolled back from ${up.version}`, failed: "last update failed", disabled: "updates off",
  };
  const phoneUrl = data.urls.find((u) => !u.includes(".local")) ?? data.urls[0];

  return (
    <Card>
      <CardHeader title="System" meta="network, access, updates" />
      <CardBody className="space-y-4">
        {moved && <p className="rounded-xl border border-accent/40 bg-accent/10 p-3 text-sm">Moving to <a className="text-accent underline" href={moved}>{moved}</a>…</p>}
        <section className="space-y-2">
          <h3 className="text-sm font-semibold">Network</h3>
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
            <span>Open on phones at</span>
            {data.urls.slice(0, 3).map((u) => <CopyText key={u} text={u} />)}
            {phoneUrl && <Button size="sm" variant="ghost" onClick={() => setQr(phoneUrl)}><QrCode size={14} /> Show QR</Button>}
          </div>
          {data.mdnsError && <p className="text-xs text-warn">The name {f.name}.local isn't announced on this PC ({data.mdnsError}). Use the IP address.</p>}
          <div className="grid gap-3 sm:grid-cols-[120px_200px_1fr] sm:items-end">
            <label className="block">
              <span className="mb-1 block text-xs text-muted">Port</span>
              <Input type="number" value={f.port} disabled={data.portLocked} onChange={(e) => set({ port: +e.target.value })} />
            </label>
            <label className="block">
              <span className="mb-1 block text-xs text-muted">Name on network</span>
              <div className="flex items-center rounded-xl border border-line bg-bg pr-3 focus-within:border-accent">
                <input className="h-10 min-w-0 flex-1 bg-transparent px-3 text-sm outline-none" value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="ptz" />
                <span className="text-xs text-dim">.local</span>
              </div>
            </label>
            <p className="pb-2 text-xs text-dim">{data.portLocked ? "The port is fixed by PTZ_PORT on this PC." : "Changing the port moves the controller right away."}</p>
          </div>
        </section>

        <section className="space-y-2 border-t border-line pt-3">
          <h3 className="text-sm font-semibold">Who can use it without a key</h3>
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone="good">✓ This PC</Badge>
            <button type="button" onClick={() => set({ trustLan: !f.trustLan })}>
              <Badge tone={f.trustLan ? "good" : "neutral"} className="cursor-pointer">{f.trustLan ? "✓" : "✗"} Venue network & Tailscale</Badge>
            </button>
            {nets.map((n) => (
              <Badge key={n} tone="good" className="normal-case tracking-normal">✓ {n}
                <button type="button" aria-label={`Remove ${n}`} onClick={() => set({ trustedCidrs: nets.filter((x) => x !== n) })}><X size={11} /></button>
              </Badge>
            ))}
            <form className="flex items-center gap-1" onSubmit={(e) => {
              e.preventDefault();
              if (newNet.trim()) set({ trustedCidrs: [...nets, newNet.trim()] });
              setNewNet("");
            }}>
              <Input className="h-8 w-44 text-xs" value={newNet} onChange={(e) => setNewNet(e.target.value)} placeholder="add network, e.g. 10.2.0.0/16" />
              <Button size="sm" variant="ghost" type="submit" disabled={!newNet.trim()}><Plus size={14} /> Add</Button>
            </form>
          </div>
          <p className="text-xs text-dim">Click "Venue network & Tailscale" to switch it off. Everyone else, and every AI agent, needs an API key (below).</p>
        </section>

        <section className="space-y-2 border-t border-line pt-3">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold">Updates</h3>
            <Button size="sm" variant="ghost" className="ml-auto" onClick={check} disabled={checking || up.state === "disabled"}>
              <RefreshCw size={14} className={checking ? "animate-spin" : ""} /> Check now
            </Button>
          </div>
          <p className="text-xs text-muted">Version <span className="font-mono">{data.version}</span> · {upText[up.state] ?? up.state}{up.lastCheck ? ` · checked ${new Date(up.lastCheck).toLocaleString()}` : ""}</p>
          {up.reason && <p className="text-xs text-dim">{up.reason}</p>}
          <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
            <label className="flex items-center gap-2"><span className="text-xs text-muted">Channel</span>
              <Segmented value={f.channel} onChange={(c) => set({ channel: c })} options={[{ value: "stable", label: "Stable" }, { value: "beta", label: "Beta" }]} />
            </label>
            <label className="flex items-center gap-2"><span className="text-xs text-muted">Install updates</span>
              <Segmented value={f.autoUpdate ? "auto" : "click"} onChange={(v) => set({ autoUpdate: v === "auto" })}
                options={[{ value: "click", label: "When I click" }, { value: "auto", label: "Automatically" }]} />
            </label>
          </div>
          {f.autoUpdate && <p className="text-xs text-dim">Never on Saturday 7 AM–6 PM, and never while a camera is moving or a macro runs.</p>}
          <button type="button" className="flex items-center gap-1 text-xs text-muted hover:text-fg" onClick={() => setAdvanced((a) => !a)}>
            {advanced ? <ChevronUp size={13} /> : <ChevronDown size={13} />} Advanced: release source
          </button>
          {advanced && (
            <div className="grid gap-3 rounded-xl border border-line p-3 sm:grid-cols-2">
              <label className="block"><span className="mb-1 block text-xs text-muted">GitHub repository (owner/name)</span>
                <Input value={f.updateRepo} onChange={(e) => set({ updateRepo: e.target.value })} placeholder="owner/ptz-controller" />
              </label>
              <label className="block"><span className="mb-1 block text-xs text-muted">Access token (private repository only)</span>
                <Input type="password" value={token} onChange={(e) => { setToken(e.target.value); setClearToken(false); }} autoComplete="new-password"
                  placeholder={data.hasToken && !clearToken ? "saved, type to change" : "none"} />
                {data.hasToken && !clearToken && <button type="button" className="mt-1 text-xs text-bad hover:underline" onClick={() => setClearToken(true)}>Remove saved token</button>}
              </label>
            </div>
          )}
        </section>

        {err && <p className="text-sm text-bad">{err}</p>}
        <div className={cn("flex justify-end gap-2", !dirty && "opacity-60")}>
          <Button variant="ghost" disabled={!dirty} onClick={() => { setF({ ...orig, trustedCidrs: orig.trustedCidrs ?? [] }); setToken(""); setClearToken(false); setErr(""); }}>Undo changes</Button>
          <Button variant="accent" disabled={!dirty || saving} onClick={save}>{saving && <Loader2 size={15} className="animate-spin" />} Save</Button>
        </div>
      </CardBody>
      <Dialog open={!!qr} onOpenChange={(o) => !o && setQr(null)} title="Open on a phone" description="Point the phone's camera at this code.">
        {qr && (
          <div className="flex flex-col items-center gap-3">
            <img src={`/api/system/qr?text=${encodeURIComponent(qr)}`} alt={qr} className="w-64 rounded-xl bg-white p-3 [image-rendering:pixelated]" />
            <CopyText text={qr} />
          </div>
        )}
      </Dialog>
    </Card>
  );
}

