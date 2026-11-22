import { useState } from "react";
import { Check, Copy, Eye, EyeOff, KeyRound, Loader2, Plus, Save, Trash2, X } from "lucide-react";
import { api } from "@/lib/api";
import { useShared, refreshShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Button, Dialog, Input } from "@/components/ui";
import { VideoCard } from "@/components/SystemSetup";
import { refusedText, type Refused } from "@/lib/refused";

// Settings → Video feeds: pictures dashboards can show. Cameras add
// their own; anything else is sent here (SRT / RTSP push) or pulled from
// an address. The controller only relays video, it never re-encodes it.
// A feed sent here takes a stream only from a sender that gives its
// publish key (user = feed id, password = key); watching needs no key.

type Status = { online: boolean; inKbps: number; viewers: number; width?: number; height?: number; lastRefused?: Refused } | undefined;
type Feed = {
  id: string; name: string; source: "camera" | "push" | "pull"; url?: string; enabled: boolean; detail?: string; status: Status;
  canEdit?: boolean; owner?: string; ownerName?: string;
  /** key, url and rtspUrl only for callers allowed to manage the feed (keyHidden otherwise). */
  push?: { host: string; port: number; rtspPort: number; user: string; streamId: string; key?: string; url?: string; rtspUrl?: string; keyHidden?: boolean };
};
type Draft = { id?: string; name: string; source: "push" | "pull"; url: string };

const BADGE = { camera: ["CAMERA", "text-info"], push: ["SENT HERE", "text-accent"], pull: ["PULLED", "text-[#A78BFA]"] } as const;

export function VideoFeedsSection() {
  const { data } = useShared<{ feeds: Feed[] }>("/api/feeds", 3000);
  const [draft, setDraft] = useState<Draft | null>(null);
  const feeds = data?.feeds ?? [];
  const status = (f: Feed) => {
    if (f.source === "camera" && !f.enabled) return <span className="text-dim">still pictures only</span>;
    if (f.status?.online) return <span className="text-good">● {f.source === "push" ? "receiving" : "live"} {(f.status.inKbps / 1000).toFixed(1)} Mbps · {f.status.viewers} watching</span>;
    if (f.source === "push" && f.status?.lastRefused) return <span className="text-warn">⚠ sender refused at {new Date(f.status.lastRefused.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</span>;
    if (f.source === "push") return <span className="text-muted">○ waiting for the sender</span>;
    return <span className="text-muted">○ idle — starts when someone watches</span>;
  };
  const panel = draft ? <FeedForm draft={draft} feed={feeds.find((f) => f.id === draft.id)} onChange={setDraft} onDone={() => setDraft(null)} /> : <Choose onPick={(source) => setDraft({ name: "", source, url: "" })} />;
  return (
    <div className="space-y-4">
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_420px]">
        <div className="min-w-0 space-y-3">
          <div className="flex items-start gap-3">
            <div className="min-w-0 flex-1">
              <h2 className="text-xl font-bold">Video feeds</h2>
              <p className="text-sm text-muted">Pictures your dashboards can show. The controller only relays them (no re-encoding); a feed costs nothing until a screen watches it.</p>
            </div>
            <Button variant="accent" className="xl:hidden" onClick={() => setDraft({ name: "", source: "push", url: "" })}><Plus size={16} /> Add feed</Button>
          </div>
          {feeds.map((f) => {
            const [label, tone] = BADGE[f.source];
            return (
              <button key={f.id} type="button" disabled={f.source === "camera"} onClick={() => f.source !== "camera" && setDraft({ id: f.id, name: f.name, source: f.source as "push" | "pull", url: f.url ?? "" })}
                className={cn("flex w-full items-center gap-3 rounded-xl border bg-panel px-3.5 py-3 text-left", draft?.id === f.id ? "border-accent" : "border-line", f.source !== "camera" && "hover:border-line-strong")}>
                <span className={cn("w-[84px] shrink-0 rounded-md bg-panel-2 px-2 py-1 text-center text-[10px] font-bold tracking-[0.08em] max-sm:w-auto", tone)}>{label}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-semibold">{f.name}</span>
                  <span className="block truncate text-xs text-muted">
                    {f.source === "camera" ? `${f.detail} · turn live video on/off under Cameras below` : f.source === "push" ? `SRT / RTSP push · needs its publish key${f.ownerName ? ` · added by ${f.ownerName}` : ""}` : `pull · ${f.url}`}
                  </span>
                  <span className="mt-0.5 block text-xs sm:hidden">{status(f)}</span>
                  {f.source === "push" && !f.status?.online && f.status?.lastRefused && (
                    <span className="mt-1 block whitespace-normal text-xs text-warn">{refusedText(f.status.lastRefused, f.id)}</span>
                  )}
                </span>
                <span className="shrink-0 text-xs max-sm:hidden">{status(f)}</span>
              </button>
            );
          })}
          <p className="text-[11px] text-dim">NDI? Not directly — browsers and the gateway can't take it. Apps that already receive NDI (vMix, OBS) can send it here by SRT.</p>
        </div>
        <aside className="hidden xl:block">{panel}</aside>
      </div>
      <Dialog open={!!draft && window.innerWidth < 1280} onOpenChange={(o) => !o && setDraft(null)} title={draft?.id ? "Edit feed" : "Add feed"} wide>
        {draft && <FeedForm draft={draft} feed={feeds.find((f) => f.id === draft.id)} onChange={setDraft} onDone={() => setDraft(null)} bare />}
      </Dialog>
      <VideoCard />
    </div>
  );
}

function Choose({ onPick }: { onPick: (s: "push" | "pull") => void }) {
  const opt = (s: "push" | "pull" | "camera", title: string, sub: string) => (
    <button type="button" onClick={() => s !== "camera" && onPick(s)} disabled={s === "camera"}
      className="w-full rounded-xl border border-line bg-panel-2 px-3 py-2.5 text-left transition enabled:hover:border-accent disabled:opacity-60">
      <span className="block text-sm font-semibold">{title}</span>
      <span className="block text-[11px] text-muted">{sub}</span>
    </button>
  );
  return (
    <div className="space-y-2 rounded-2xl border border-accent/60 bg-panel p-4">
      <h3 className="text-base font-bold">Add feed</h3>
      <p className="text-xs text-muted">Where does the picture come from?</p>
      {opt("push", "Sent to this controller (SRT / RTSP push)", "vMix, OBS or an encoder connects here with its own key; you get the address to paste into it")}
      {opt("camera", "A camera", "added for you with each camera under Settings → Cameras")}
      {opt("pull", "Pulled from an address (RTSP / SRT)", "the controller connects to a camera, encoder or another server")}
    </div>
  );
}

function FeedForm({ draft, feed, onChange, onDone, bare }: { draft: Draft; feed?: Feed; onChange: (d: Draft) => void; onDone: () => void; bare?: boolean }) {
  const { log } = useLog();
  const [busy, setBusy] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [rekey, setRekey] = useState(false);
  const [show, setShow] = useState(false);
  const locked = feed?.canEdit === false;
  const newKey = async () => {
    setRekey(false);
    setBusy("key");
    try { await api(`/api/feeds/${draft.id}/key`, "POST"); log("Settings", `Feed ${draft.name}: new publish key — paste it into the sender`); refreshShared("/api/feeds"); setShow(true); }
    catch (e) { log("Settings", (e as Error).message, false); }
    finally { setBusy(""); }
  };
  const save = async () => {
    setBusy("save");
    try {
      const saved = await api<{ id: string }>(draft.id ? `/api/feeds/${draft.id}` : "/api/feeds", draft.id ? "PUT" : "POST", draft);
      log("Settings", `Feed ${draft.name} saved`);
      refreshShared("/api/feeds");
      // A new "sent here" feed stays open: its SRT settings are what you need next.
      if (!draft.id) onChange({ ...draft, id: saved.id });
    } catch (e) { log("Settings", (e as Error).message, false); }
    finally { setBusy(""); }
  };
  const remove = async () => {
    setConfirm(false);
    try { await api(`/api/feeds/${draft.id}`, "DELETE"); log("Settings", `Feed ${draft.name} removed`); refreshShared("/api/feeds"); onDone(); }
    catch (e) { log("Settings", (e as Error).message, false); }
  };
  return (
    <div className={cn("space-y-3", !bare && "rounded-2xl border border-accent/60 bg-panel p-4")}>
      <div className="flex items-center gap-2">
        <h3 className="flex-1 text-base font-bold">{draft.id ? draft.name : draft.source === "push" ? "Sent to this controller" : "Pulled from an address"}</h3>
        {!bare && <Button size="icon" variant="ghost" onClick={onDone} aria-label="Close"><X size={16} /></Button>}
      </div>
      <label className="block">
        <span className="mb-1 block text-xs font-medium text-muted">Name</span>
        <Input value={draft.name} maxLength={40} onChange={(e) => onChange({ ...draft, name: e.target.value })} placeholder={draft.source === "push" ? "Program, Slides…" : "Lobby camera"} />
      </label>
      {draft.source === "pull" && (
        <label className="block">
          <span className="mb-1 block text-xs font-medium text-muted">Address</span>
          <Input value={draft.url} onChange={(e) => onChange({ ...draft, url: e.target.value })} placeholder="rtsp://192.168.1.60/stream1 or srt://…" />
        </label>
      )}
      {draft.source === "push" && (feed?.push ? <PushInfo feed={feed} show={show} onShow={setShow} onNewKey={locked ? undefined : () => setRekey(true)} busy={busy === "key"} />
        : <p className="text-xs text-muted">Save it to get the address and key to paste into vMix, OBS or the encoder.</p>)}
      {feed?.ownerName && <p className="text-[11px] text-dim">Added by the {feed.ownerName} add-on connection.</p>}
      {locked && <p className="text-[11px] text-warn">You can't change this feed from here.</p>}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="accent" onClick={save} disabled={!!busy || locked || !draft.name.trim()}>{busy ? <Loader2 size={15} className="animate-spin" /> : <Save size={15} />} Save</Button>
        {draft.id && !locked && <Button variant="ghost" className="ml-auto text-bad" onClick={() => setConfirm(true)}><Trash2 size={15} /> Remove</Button>}
      </div>
      <Dialog open={confirm} onOpenChange={setConfirm} title={`Remove the ${draft.name} feed?`} description="Video widgets showing it will say it's gone until you pick another feed.">
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirm(false)}>Cancel</Button>
          <Button variant="danger" onClick={remove}><Trash2 size={15} /> Remove</Button>
        </div>
      </Dialog>
      <Dialog open={rekey} onOpenChange={setRekey} title={`New publish key for ${draft.name}?`}
        description="The old key stops working: the sender can't connect again until you paste the new Stream ID or address into it.">
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setRekey(false)}>Cancel</Button>
          <Button variant="danger" onClick={newKey}><KeyRound size={15} /> New key</Button>
        </div>
      </Dialog>
    </div>
  );
}

/** What a sender needs for a feed sent here: SRT (vMix, OBS) or RTSP (ffmpeg, encoders), each with the feed's key. */
function PushInfo({ feed, show, onShow, onNewKey, busy }: { feed: Feed; show: boolean; onShow: (v: boolean) => void; onNewKey?: () => void; busy: boolean }) {
  const p = feed.push!;
  if (p.keyHidden || !p.key) {
    return (
      <div className="space-y-2 rounded-xl bg-bg p-3 text-xs">
        <p className="text-muted">This feed takes a stream only with its publish key, and you can't see it here (it needs the “Manage video feeds” permission).</p>
        <Kv k="Host" v={p.host} /><Kv k="SRT port" v={String(p.port)} />
      </div>
    );
  }
  const key = p.key;
  const mask = (v: string) => (show ? v : v.split(key).join("••••••••"));
  return (
    <div className="space-y-3 rounded-xl bg-bg p-3 text-xs">
      <p className="rounded-lg border border-warn/40 bg-warn/10 px-2.5 py-2 text-warn">
        A sender must give this feed's key or the stream is refused. Sent here before keys? Paste the new Stream ID (or address) below into it.
      </p>
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <span className="flex-1 text-[10px] font-bold uppercase tracking-[0.12em] text-dim">SRT — vMix, OBS</span>
          <button type="button" className="flex items-center gap-1 text-muted hover:text-fg" onClick={() => onShow(!show)}>
            {show ? <EyeOff size={13} /> : <Eye size={13} />} {show ? "Hide key" : "Show key"}
          </button>
        </div>
        <p className="text-muted">vMix: Settings → Outputs/NDI/SRT → an output → SRT, Type Caller; paste Host, Port and Stream ID. OBS: Settings → Stream → Custom, paste the full address.</p>
        <Kv k="Host" v={p.host} /><Kv k="Port" v={String(p.port)} /><Kv k="Stream ID" v={p.streamId} shown={mask(p.streamId)} /><Kv k="Full address" v={p.url ?? ""} shown={mask(p.url ?? "")} />
      </div>
      <div className="space-y-2">
        <span className="block text-[10px] font-bold uppercase tracking-[0.12em] text-dim">RTSP — ffmpeg, encoders</span>
        <Kv k="Address" v={p.rtspUrl ?? ""} shown={mask(p.rtspUrl ?? "")} /><Kv k="User" v={p.user} /><Kv k="Key" v={key} shown={mask(key)} />
      </div>
      {!feed.status?.online && feed.status?.lastRefused && (
        <p role="status" className="rounded-lg border border-warn/40 bg-warn/10 px-2.5 py-2 text-warn">{refusedText(feed.status.lastRefused, feed.id)}</p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <p className={cn("flex-1", feed.status?.online ? "text-good" : "text-muted")}>{feed.status?.online ? "● Receiving now." : "Shows “receiving” here the moment the sender connects."}</p>
        {onNewKey && <Button size="sm" variant="ghost" onClick={onNewKey} disabled={busy}>{busy ? <Loader2 size={14} className="animate-spin" /> : <KeyRound size={14} />} New key</Button>}
      </div>
    </div>
  );
}

function Kv({ k, v, shown }: { k: string; v: string; shown?: string }) {
  const [ok, setOk] = useState(false);
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 text-dim">{k}</span>
      <code className="min-w-0 flex-1 truncate rounded bg-panel-2 px-1.5 py-0.5 font-mono text-[11px]" title={shown ?? v}>{shown ?? v}</code>
      <button type="button" aria-label={`Copy ${k}`} className="text-muted hover:text-fg" onClick={() => { navigator.clipboard?.writeText(v); setOk(true); setTimeout(() => setOk(false), 1500); }}>
        {ok ? <Check size={13} /> : <Copy size={13} />}
      </button>
    </div>
  );
}
