import { useState } from "react";
import { Check, Copy, Loader2, Plus, Save, Trash2, X } from "lucide-react";
import { api } from "@/lib/api";
import { useShared, refreshShared } from "@/lib/hooks";
import { useLog } from "@/lib/log";
import { cn } from "@/lib/cn";
import { Button, Dialog, Input } from "@/components/ui";
import { VideoCard } from "@/components/SystemSetup";

// Settings → Video feeds: pictures dashboards can show. Cameras add
// their own; anything else is sent here (SRT push) or pulled from an
// address. The controller only relays video, it never re-encodes it.

type Status = { online: boolean; inKbps: number; viewers: number; width?: number; height?: number } | undefined;
type Feed = {
  id: string; name: string; source: "camera" | "push" | "pull"; url?: string; enabled: boolean; detail?: string; status: Status;
  push?: { url: string; host: string; port: number; streamId: string };
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
                    {f.source === "camera" ? `${f.detail} · turn live video on/off under Cameras below` : f.source === "push" ? `SRT push · stream id ${f.push?.streamId}` : `pull · ${f.url}`}
                  </span>
                  <span className="mt-0.5 block text-xs sm:hidden">{status(f)}</span>
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
      {opt("push", "Sent to this controller (SRT push)", "vMix, OBS or an encoder connects here; you get the address to paste into it")}
      {opt("camera", "A camera", "added for you with each camera under Settings → Cameras")}
      {opt("pull", "Pulled from an address (RTSP / SRT)", "the controller connects to a camera, encoder or another server")}
    </div>
  );
}

function FeedForm({ draft, feed, onChange, onDone, bare }: { draft: Draft; feed?: Feed; onChange: (d: Draft) => void; onDone: () => void; bare?: boolean }) {
  const { log } = useLog();
  const [busy, setBusy] = useState("");
  const [confirm, setConfirm] = useState(false);
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
      {draft.source === "push" && (feed?.push ? (
        <div className="space-y-2 rounded-xl bg-bg p-3 text-xs">
          <p className="text-muted">Paste into the sender — vMix: Settings → Outputs/NDI/SRT → an output → SRT (Type: Caller). OBS: Settings → Stream → Custom.</p>
          <Kv k="Host" v={feed.push.host} /><Kv k="Port" v={String(feed.push.port)} /><Kv k="Stream ID" v={feed.push.streamId} /><Kv k="Full address" v={feed.push.url} />
          <p className={feed.status?.online ? "text-good" : "text-muted"}>{feed.status?.online ? "● Receiving now." : "Shows “receiving” here the moment the sender connects."}</p>
        </div>
      ) : <p className="text-xs text-muted">Save it to get the address to paste into vMix, OBS or the encoder.</p>)}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="accent" onClick={save} disabled={!!busy || !draft.name.trim()}>{busy ? <Loader2 size={15} className="animate-spin" /> : <Save size={15} />} Save</Button>
        {draft.id && <Button variant="ghost" className="ml-auto text-bad" onClick={() => setConfirm(true)}><Trash2 size={15} /> Remove</Button>}
      </div>
      <Dialog open={confirm} onOpenChange={setConfirm} title={`Remove the ${draft.name} feed?`} description="Video widgets showing it will say it's gone until you pick another feed.">
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setConfirm(false)}>Cancel</Button>
          <Button variant="danger" onClick={remove}><Trash2 size={15} /> Remove</Button>
        </div>
      </Dialog>
    </div>
  );
}

function Kv({ k, v }: { k: string; v: string }) {
  const [ok, setOk] = useState(false);
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 text-dim">{k}</span>
      <code className="min-w-0 flex-1 truncate rounded bg-panel-2 px-1.5 py-0.5 font-mono text-[11px]">{v}</code>
      <button type="button" aria-label={`Copy ${k}`} className="text-muted hover:text-fg" onClick={() => { navigator.clipboard?.writeText(v); setOk(true); setTimeout(() => setOk(false), 1500); }}>
        {ok ? <Check size={13} /> : <Copy size={13} />}
      </button>
    </div>
  );
}
