import { useEffect, useRef, useState } from "react";
import { Plus, Star, Pencil, Trash2, RefreshCw, Loader2, CheckCircle2, XCircle, ChevronDown, ChevronUp, Radar, KeyRound } from "lucide-react";
import { api, type CameraInfo, type FoundCamera, type ProbeResult } from "@/lib/api";
import { refreshShared, useShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Badge, Button, Card, CardBody, CardHeader, Dialog, Dot, Empty, Input, Select } from "@/components/ui";

export function useCameras() {
  return useShared<CameraInfo[]>("/api/cameras", 4000);
}

function model(c: CameraInfo) {
  const d = c.status?.device ?? {};
  const fw = (d.versioninfo ?? "").match(/v?(\d+\.\d+\.\d+)/)?.[1];
  return [d.devname, c.host, fw && `firmware ${fw}`].filter(Boolean).join(" · ");
}

/** A camera's live thumbnail, refreshed every few seconds. */
function Thumb({ cam, className }: { cam: CameraInfo; className?: string }) {
  const [t, setT] = useState(() => Date.now());
  const [bad, setBad] = useState(false);
  const on = !!cam.status?.connected;
  useEffect(() => {
    if (!on) return;
    const i = window.setInterval(() => !document.hidden && setT(Date.now()), 8000);
    return () => clearInterval(i);
  }, [on]);
  useEffect(() => setBad(false), [t, on]);
  return (
    <div className={cn("relative flex aspect-video shrink-0 items-center justify-center overflow-hidden rounded-lg bg-bg", className)}>
      {on && !bad ? (
        <img src={`/api/snapshot?cam=${cam.id}&t=${t}`} alt="" className="h-full w-full object-cover" onError={() => setBad(true)} />
      ) : (
        <span className="text-[10px] text-dim">no picture</span>
      )}
    </div>
  );
}

/** Settings → Cameras. */
export function CamerasCard() {
  const { data: list, error } = useCameras();
  const [editing, setEditing] = useState<CameraInfo | "new" | null>(null);
  const [removing, setRemoving] = useState<CameraInfo | null>(null);
  const { log } = useLog();
  const reload = () => refreshShared("/api/cameras");

  const makeMain = async (c: CameraInfo) => {
    try {
      await api(`/api/cameras/${c.id}/main`, "POST");
      log("Cameras", `${c.name} is now the main camera`);
    } catch (e) {
      log("Cameras", (e as Error).message, false);
    }
    reload();
  };
  const remove = async () => {
    const c = removing!;
    setRemoving(null);
    try {
      await api(`/api/cameras/${c.id}`, "DELETE");
      log("Cameras", `Removed ${c.name}`);
    } catch (e) {
      log("Cameras", (e as Error).message, false);
    }
    reload();
  };

  return (
    <Card>
      <CardHeader title="Cameras" meta="add by IP or find on the network">
        <Button size="sm" variant="accent" onClick={() => setEditing("new")}><Plus size={14} /> Add camera</Button>
      </CardHeader>
      <CardBody className="space-y-2">
        {error ? <Empty>{error}</Empty> : !list ? <Empty>Loading…</Empty> : list.length === 0 ? (
          <Empty>No camera yet. <button className="text-accent underline-offset-2 hover:underline" onClick={() => setEditing("new")}>Add one</button>: it can find cameras on this network.</Empty>
        ) : list.map((c) => (
          <div key={c.id} className="flex flex-wrap items-center gap-3 rounded-xl border border-line bg-panel-2 p-3">
            <Thumb cam={c} className="w-24 sm:w-28" />
            <div className="min-w-0 flex-1 basis-40">
              <div className="truncate text-sm font-semibold">{c.name}</div>
              <div className="truncate text-xs text-muted">{model(c) || c.host}</div>
              <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
                <span className={cn("flex items-center gap-1.5", c.status?.connected ? "text-good" : "text-bad")}>
                  <Dot on={!!c.status?.connected} />
                  {c.status?.connected ? "Connected" : "Not answering, powered off? (retrying)"}
                </span>
                {c.main && <Badge tone="accent"><Star size={10} /> main camera</Badge>}
                {!c.video && <Badge>no live video</Badge>}
              </div>
            </div>
            <div className="flex items-center gap-1 max-sm:w-full max-sm:justify-end">
              {!c.main && <Button size="sm" variant="ghost" onClick={() => makeMain(c)}><Star size={14} /> Make main</Button>}
              <Button size="sm" variant="ghost" onClick={() => setEditing(c)}><Pencil size={14} /> Edit</Button>
              <Button size="icon" variant="ghost" className="text-bad" aria-label={`Remove ${c.name}`} onClick={() => setRemoving(c)}><Trash2 size={15} /></Button>
            </div>
          </div>
        ))}
        {list && list.length > 1 && (
          <p className="pt-1 text-xs text-dim">Each camera widget picks one of these cameras in its own settings. Presets belong to their camera. Macros and agents use the main camera.</p>
        )}
      </CardBody>
      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)} wide
        title={editing === "new" ? "Add a camera" : `Edit ${(editing as CameraInfo | null)?.name ?? ""}`}>
        {editing && (
          <CameraForm initial={editing === "new" ? undefined : editing} onCancel={() => setEditing(null)}
            onSaved={(c) => {
              setEditing(null);
              log("Cameras", `${editing === "new" ? "Added" : "Saved"} ${c.name}`);
              reload();
            }} />
        )}
      </Dialog>
      <Dialog open={!!removing} onOpenChange={(o) => !o && setRemoving(null)} title={`Remove ${removing?.name}?`}
        description="The controller stops talking to it. Its preset names stay on this PC, and the positions stay in the camera.">
        <div className="flex justify-end gap-2"><Button variant="ghost" onClick={() => setRemoving(null)}>Cancel</Button><Button variant="danger" onClick={remove}>Remove</Button></div>
      </Dialog>
    </Card>
  );
}

type Form = {
  name: string; type: string; host: string; viscaPort: number; httpPort: number; rtspPort: number;
  streamHd: string; streamSd: string; user: string; pass: string; video: boolean;
};

const blank: Form = { name: "", type: "ptzoptics", host: "", viscaPort: 5678, httpPort: 80, rtspPort: 554, streamHd: "/1", streamSd: "/2", user: "", pass: "", video: true };

function Field({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <label className={cn("block", className)}>
      <span className="mb-1 block text-xs text-muted">{label}</span>
      {children}
    </label>
  );
}

/** Find on the network, or type the IP; Test shows the camera's own picture. */
export function CameraForm({ initial, onSaved, onCancel, saveLabel, defaultName }: {
  initial?: CameraInfo; onSaved: (c: CameraInfo) => void; onCancel?: () => void; saveLabel?: string; defaultName?: string;
}) {
  const [f, setF] = useState<Form>(() => initial ? { ...blank, ...initial, pass: "" } : { ...blank, name: defaultName ?? "" });
  const [advanced, setAdvanced] = useState(false);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<ProbeResult | null>(null);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState("");
  const userRef = useRef<HTMLInputElement>(null);
  const set = (p: Partial<Form>) => {
    setF((o) => ({ ...o, ...p }));
    setErr("");
  };

  const test = async (form = f) => {
    if (!form.host.trim()) {
      setErr("Type the camera's IP address first.");
      return null;
    }
    setTesting(true);
    setResult(null);
    try {
      const r = await api<ProbeResult>("/api/cameras/test", "POST", { ...form, id: initial?.id });
      setResult(r);
      if (r.login === "needed") userRef.current?.focus();
      return r;
    } catch (e) {
      setErr((e as Error).message);
      return null;
    } finally {
      setTesting(false);
    }
  };

  const save = async () => {
    setSaving(true);
    setErr("");
    try {
      const body = { ...f, name: f.name.trim() || result?.model || "Camera" };
      const list = initial
        ? await api<CameraInfo[]>(`/api/cameras/${initial.id}`, "PUT", body)
        : await api<CameraInfo[]>("/api/cameras", "POST", body);
      const saved = initial ? list.find((c) => c.id === initial.id) : list.find((c) => c.host === f.host.trim() && c.viscaPort === f.viscaPort);
      onSaved(saved ?? list[list.length - 1]);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const pick = (c: FoundCamera) => {
    const next = { ...f, host: c.host, viscaPort: c.viscaPort, httpPort: c.httpPort, name: f.name || c.model || "" };
    setF(next);
    if (!c.login) test(next);
    else {
      setResult(null);
      setTimeout(() => userRef.current?.focus(), 0);
    }
  };

  return (
    <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); save(); }}>
      {!initial && <Finder onPick={pick} picked={`${f.host}:${f.viscaPort}`} />}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Name" className="sm:col-span-2">
          <Input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="e.g. Sanctuary, Balcony" maxLength={40} />
        </Field>
        <Field label="Type">
          <Select value={f.type} onChange={(v) => set({ type: v })} options={[{ value: "ptzoptics", label: "PTZOptics (VISCA over IP)" }]} />
        </Field>
        <Field label="IP address">
          <Input value={f.host} onChange={(e) => { set({ host: e.target.value }); setResult(null); }} placeholder="10.1.10.110" inputMode="decimal" autoComplete="off" />
        </Field>
        <Field label="Camera login user (newer firmware needs it)">
          <Input ref={userRef} value={f.user} onChange={(e) => set({ user: e.target.value })} placeholder="admin" autoComplete="off" />
        </Field>
        <Field label="Camera login password">
          <Input type="password" value={f.pass} onChange={(e) => set({ pass: e.target.value })} autoComplete="new-password"
            placeholder={initial?.hasPassword ? "saved, type to change" : ""} />
        </Field>
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-dim">
        <span className="flex items-center gap-1"><KeyRound size={12} /> Kept on this PC only, never shown again.</span>
        <button type="button" className="flex items-center gap-1 text-muted hover:text-fg" onClick={() => setAdvanced((v) => !v)}>
          {advanced ? <ChevronUp size={13} /> : <ChevronDown size={13} />} Advanced (ports)
        </button>
      </div>
      {advanced && (
        <div className="grid gap-3 rounded-xl border border-line p-3 sm:grid-cols-3">
          <Field label="Control port (VISCA TCP)"><Input type="number" value={f.viscaPort} onChange={(e) => set({ viscaPort: +e.target.value })} /></Field>
          <Field label="Web port"><Input type="number" value={f.httpPort} onChange={(e) => set({ httpPort: +e.target.value })} /></Field>
          <Field label="RTSP port"><Input type="number" value={f.rtspPort} onChange={(e) => set({ rtspPort: +e.target.value })} /></Field>
          <Field label="HD stream path"><Input value={f.streamHd} onChange={(e) => set({ streamHd: e.target.value })} /></Field>
          <Field label="SD stream path"><Input value={f.streamSd} onChange={(e) => set({ streamSd: e.target.value })} /></Field>
        </div>
      )}

      {(testing || result) && <TestResult testing={testing} r={result} />}
      {err && <p className="text-sm text-bad">{err}</p>}

      <div className="flex flex-wrap items-center gap-2">
        <Button onClick={() => test()} disabled={testing || !f.host.trim()}>
          {testing ? <Loader2 size={15} className="animate-spin" /> : <RefreshCw size={15} />} {result ? "Test again" : "Test"}
        </Button>
        <div className="ml-auto flex gap-2">
          {onCancel && <Button variant="ghost" onClick={onCancel}>Cancel</Button>}
          <Button type="submit" variant="accent" disabled={saving || !f.host.trim()}>
            {saving && <Loader2 size={15} className="animate-spin" />}
            {saveLabel ?? (initial ? "Save" : result && !result.ok ? "Add anyway" : "Add camera")}
          </Button>
        </div>
      </div>
    </form>
  );
}

function Check({ ok, children }: { ok: boolean; children: React.ReactNode }) {
  return (
    <div className={cn("flex items-start gap-1.5", ok ? "text-good" : "text-bad")}>
      {ok ? <CheckCircle2 size={14} className="mt-0.5 shrink-0" /> : <XCircle size={14} className="mt-0.5 shrink-0" />}
      <span>{children}</span>
    </div>
  );
}

function TestResult({ testing, r }: { testing: boolean; r: ProbeResult | null }) {
  if (testing || !r) {
    return (
      <div className="flex items-center gap-2 rounded-xl border border-line bg-panel-2 p-4 text-sm text-muted">
        <Loader2 size={16} className="animate-spin" /> Talking to the camera…
      </div>
    );
  }
  const loginOk = r.login === "ok" || r.login === "none";
  return (
    <div className={cn("flex flex-wrap gap-4 rounded-xl border p-3", r.ok ? "border-good/30 bg-good/5" : "border-bad/30 bg-bad/5")}>
      <div className="flex aspect-video w-full items-center justify-center overflow-hidden rounded-lg bg-bg sm:w-48">
        {r.snapshot ? <img src={r.snapshot} alt="What the camera sees" className="h-full w-full object-cover" /> : <span className="text-xs text-dim">no picture</span>}
      </div>
      <div className="min-w-0 flex-1 space-y-1 text-sm">
        <div className={cn("font-semibold", r.ok ? "text-good" : "text-bad")}>{r.ok ? "✓ " : ""}{r.message}</div>
        {(r.model || r.firmware) && <div className="text-xs text-muted">{[r.model, r.firmware && `firmware ${r.firmware}`].filter(Boolean).join(" · ")}</div>}
        <div className="space-y-0.5 pt-1 text-xs">
          <Check ok={r.control}>control (VISCA){r.controlError ? `: ${r.controlError}` : ""}</Check>
          <Check ok={r.picture}>picture (snapshot){r.pictureError ? `: ${r.pictureError}` : ""}</Check>
          {r.login !== "unknown" && <Check ok={loginOk}>{r.login === "none" ? "no login needed" : r.login === "ok" ? "login accepted" : r.login === "needed" ? "needs a login" : "login rejected"}</Check>}
        </div>
      </div>
    </div>
  );
}

/** "Found on this network": scans this PC's networks for cameras. */
function Finder({ onPick, picked }: { onPick: (c: FoundCamera) => void; picked: string }) {
  const [found, setFound] = useState<FoundCamera[] | null>(null);
  const [scanning, setScanning] = useState(false);
  const [err, setErr] = useState("");
  const scan = async () => {
    setScanning(true);
    setErr("");
    try {
      const r = await api<{ found: FoundCamera[] }>("/api/cameras/scan");
      setFound(r.found);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setScanning(false);
    }
  };
  useEffect(() => {
    scan();
  }, []);
  const fresh = (found ?? []).filter((c) => !c.known);
  return (
    <div className="rounded-xl border border-line bg-bg p-3">
      <div className="mb-2 flex items-center gap-2">
        <Radar size={14} className={cn("text-muted", scanning && "animate-pulse text-accent")} />
        <span className="text-xs font-semibold">Found on this network</span>
        <button type="button" className="ml-auto flex items-center gap-1 text-xs text-muted hover:text-fg disabled:opacity-40" onClick={scan} disabled={scanning}>
          <RefreshCw size={12} className={scanning ? "animate-spin" : ""} /> {scanning ? "Looking…" : "Scan again"}
        </button>
      </div>
      {err ? <p className="text-xs text-bad">{err}</p> : scanning && !found ? (
        <p className="text-xs text-muted">Looking for cameras (takes a few seconds)…</p>
      ) : fresh.length === 0 ? (
        <p className="text-xs text-muted">
          {found && found.length > 0 ? "Every camera found is already set up." : "None found. Type the camera's IP address below. It's printed on a sticker, or shown on screen when the camera starts."}
        </p>
      ) : (
        <div className="grid gap-2 sm:grid-cols-2">
          {fresh.map((c) => (
            <button type="button" key={`${c.host}:${c.viscaPort}`} onClick={() => onPick(c)}
              className={cn("rounded-lg border px-3 py-2 text-left transition",
                picked === `${c.host}:${c.viscaPort}` ? "border-accent bg-accent/10" : "border-line bg-panel-2 hover:border-line-strong")}>
              <div className="text-sm font-semibold">{c.model || "PTZ camera"} · {c.host}</div>
              <div className="text-xs text-muted">{c.login ? "needs its login" : `control port ${c.viscaPort} open`} · tap to use</div>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
