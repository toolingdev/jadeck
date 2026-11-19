import { useState } from "react";
import { KeyRound, Plus, Copy, Check, Ban, Trash2, SlidersHorizontal } from "lucide-react";
import { api, type ApiKey, type AuditEntry, type Scope } from "@/lib/api";
import { usePoll } from "@/lib/hooks";
import { DeviceSetup } from "@/components/DeviceSetup";
import { DashboardSettings } from "@/components/DashboardSettings";
import { CamerasCard } from "@/components/CameraSetup";
import { SystemCard, VideoCard } from "@/components/SystemSetup";
import { useLog } from "@/lib/log";
import { Badge, Button, Card, CardBody, CardHeader, Dialog, Empty, Input, Switch } from "@/components/ui";

export function SettingsPage() {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)] gap-3 lg:grid-cols-2 [&>*]:min-w-0">
      <div className="lg:col-span-2"><CamerasCard /></div>
      <div className="lg:col-span-2"><DeviceSetup /></div>
      <VideoCard />
      <SystemCard />
      <div className="lg:col-span-2"><DashboardSettings /></div>
      <div className="lg:col-span-2"><Keys /></div>
      <Connect />
      <Audit />
      <div className="lg:col-span-2 flex justify-center pt-2">
        <a href="/setup" className="text-xs text-muted underline-offset-2 hover:text-fg hover:underline">Run the setup wizard again</a>
      </div>
    </div>
  );
}

// ------------------------------------------------------------------ keys

function Keys() {
  const { data, error, reload } = usePoll<{ keys: ApiKey[]; scopes: Scope[] }>("/api/keys", 15000);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<ApiKey | null>(null);
  const [secret, setSecret] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<{ k: ApiKey; what: "revoke" | "delete" } | null>(null);
  const { log } = useLog();
  const scopes = data?.scopes ?? [];

  const patch = async (k: ApiKey, body: object) => {
    try {
      await api(`/api/keys/${k.id}`, "PATCH", body);
    } catch (e) {
      log("Keys", (e as Error).message, false);
    }
    reload();
  };
  const doConfirm = async () => {
    if (!confirm) return;
    const { k, what } = confirm;
    setConfirm(null);
    try {
      if (what === "revoke") await api(`/api/keys/${k.id}/revoke`, "POST");
      else await api(`/api/keys/${k.id}`, "DELETE");
      log("Keys", `${what === "revoke" ? "Revoked" : "Deleted"} “${k.name}”`);
    } catch (e) {
      log("Keys", (e as Error).message, false);
    }
    reload();
  };

  return (
    <Card>
      <CardHeader title="API keys" meta="for agents and outside access">
        <Button size="sm" variant="accent" onClick={() => setCreating(true)} disabled={!!error}><Plus size={14} /> New key</Button>
      </CardHeader>
      <CardBody className="space-y-2">
        <p className="text-xs text-muted">Nobody on this PC, the venue network or Tailscale needs a key. Keys are for agents (MCP always needs one) and for access from anywhere else, and they only allow what you switch on.</p>
        {error ? <Empty>{error}</Empty> : (data?.keys ?? []).length === 0 ? <Empty>No keys yet.</Empty> : (
          <div className="space-y-2">
            {data!.keys.map((k) => (
              <div key={k.id} className="flex flex-wrap items-center gap-3 rounded-xl border border-line bg-panel-2 p-3">
                <KeyRound size={18} className={k.revokedAt ? "text-dim" : k.enabled ? "text-accent" : "text-muted"} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-semibold">{k.name}</span>
                    <span className="font-mono text-[11px] text-dim">ptz_{k.prefix}…</span>
                    {k.revokedAt ? <Badge tone="bad">Revoked</Badge> : !k.enabled ? <Badge>Off</Badge> : <Badge tone="good">Active</Badge>}
                  </div>
                  <div className="mt-1 flex flex-wrap gap-1">
                    {k.scopes.length === 0 ? <span className="text-xs text-dim">no permissions</span> :
                      k.scopes.map((s) => <Badge key={s} tone="neutral" className="normal-case tracking-normal">{scopes.find((x) => x.id === s)?.name ?? s}</Badge>)}
                  </div>
                  <div className="mt-1 text-[11px] text-dim">
                    Created {new Date(k.createdAt).toLocaleDateString()} · {k.lastUsedAt ? `last used ${new Date(k.lastUsedAt).toLocaleString()} from ${k.lastIp}` : "never used"}
                  </div>
                </div>
                {!k.revokedAt && (
                  <>
                    <Switch checked={k.enabled} onChange={(v) => patch(k, { enabled: v })} label={`${k.name} enabled`} />
                    <Button size="sm" variant="ghost" onClick={() => setEditing(k)}><SlidersHorizontal size={14} /> Permissions</Button>
                    <Button size="sm" variant="ghost" onClick={() => setConfirm({ k, what: "revoke" })}><Ban size={14} /> Revoke</Button>
                  </>
                )}
                <Button size="icon" variant="ghost" className="text-bad" onClick={() => setConfirm({ k, what: "delete" })} aria-label="Delete"><Trash2 size={15} /></Button>
              </div>
            ))}
          </div>
        )}
      </CardBody>

      {creating && <KeyDialog scopes={scopes} onClose={() => setCreating(false)} onSave={async (name, sc) => {
        const r = await api<{ secret: string }>("/api/keys", "POST", { name, scopes: sc });
        setCreating(false);
        setSecret(r.secret);
        log("Keys", `Created “${name}”`);
        reload();
      }} />}
      {editing && <KeyDialog scopes={scopes} keyItem={editing} onClose={() => setEditing(null)} onSave={async (name, sc) => {
        await api(`/api/keys/${editing.id}`, "PATCH", { name, scopes: sc });
        setEditing(null);
        log("Keys", `Updated “${name}”`);
        reload();
      }} />}
      <Dialog open={!!secret} onOpenChange={(o) => !o && setSecret(null)} title="Copy your key now" description="This is the only time it is shown. Store it in the agent's config or your password manager.">
        <SecretBox value={secret ?? ""} />
        <div className="mt-4 flex justify-end"><Button variant="accent" onClick={() => setSecret(null)}>I copied it</Button></div>
      </Dialog>
      <Dialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}
        title={confirm?.what === "revoke" ? `Revoke “${confirm?.k.name}”?` : `Delete “${confirm?.k.name}”?`}
        description={confirm?.what === "revoke" ? "It stops working immediately and can't be turned back on. It stays listed for the audit trail." : "It stops working immediately and leaves the list. Its audit entries stay."}>
        <div className="flex justify-end gap-2"><Button variant="ghost" onClick={() => setConfirm(null)}>Cancel</Button><Button variant="danger" onClick={doConfirm}>{confirm?.what === "revoke" ? "Revoke" : "Delete"}</Button></div>
      </Dialog>
    </Card>
  );
}

function KeyDialog({ scopes, keyItem, onClose, onSave }: { scopes: Scope[]; keyItem?: ApiKey; onClose: () => void; onSave: (name: string, scopes: string[]) => Promise<void> }) {
  const [name, setName] = useState(keyItem?.name ?? "");
  const [on, setOn] = useState<Set<string>>(new Set(keyItem?.scopes ?? ["camera.read"]));
  const [err, setErr] = useState("");
  const toggle = (id: string, v: boolean) => setOn((s) => {
    const n = new Set(s);
    if (v) n.add(id);
    else n.delete(id);
    return n;
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={keyItem ? "Key permissions" : "New API key"} wide
      description="Switch on only what this agent needs. You can change it any time.">
      <form className="space-y-3" onSubmit={async (e) => {
        e.preventDefault();
        try {
          await onSave(name, [...on]);
        } catch (er) {
          setErr((er as Error).message);
        }
      }}>
        <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Name, e.g. Claude — camera only" maxLength={60} />
        <div className="divide-y divide-line rounded-xl border border-line">
          {scopes.map((s) => (
            <label key={s.id} className="flex cursor-pointer items-center gap-3 px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{s.name} <span className="font-mono text-[10px] text-dim">{s.id}</span></div>
                <div className="text-xs text-muted">{s.description}</div>
              </div>
              <Switch checked={on.has(s.id)} onChange={(v) => toggle(s.id, v)} label={s.name} />
            </label>
          ))}
        </div>
        {err && <p className="text-sm text-bad">{err}</p>}
        <div className="flex justify-end gap-2"><Button variant="ghost" onClick={onClose}>Cancel</Button><Button type="submit" variant="accent" disabled={!name.trim()}>{keyItem ? "Save" : "Create key"}</Button></div>
      </form>
    </Dialog>
  );
}

function SecretBox({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-center gap-2 rounded-xl border border-accent/40 bg-bg p-2">
      <code className="min-w-0 flex-1 break-all px-1 font-mono text-xs text-accent">{value}</code>
      <Button size="sm" onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch { /* select manually */ }
      }}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? "Copied" : "Copy"}</Button>
    </div>
  );
}

// --------------------------------------------------------------- connect

function Connect() {
  const url = `${location.protocol}//${location.host}/mcp`;
  const cmd = `claude mcp add --transport http ptz ${url} --header "Authorization: Bearer ptz_…"`;
  return (
    <Card>
      <CardHeader title="Connect an agent" meta="MCP" />
      <CardBody className="space-y-3 text-sm">
        <p className="text-muted">Any MCP client that speaks Streamable HTTP can connect. It sees only the tools its key allows. Camera moves are bounded per call, and mixer changes are read back from the console.</p>
        <div><div className="mb-1 text-xs text-dim">Endpoint</div><code className="block rounded-lg bg-bg px-3 py-2 font-mono text-xs">{url}</code></div>
        <div><div className="mb-1 text-xs text-dim">Claude Code</div><code className="block break-all rounded-lg bg-bg px-3 py-2 font-mono text-xs">{cmd}</code></div>
      </CardBody>
    </Card>
  );
}

// ----------------------------------------------------------------- audit

function Audit() {
  const { data, error } = usePoll<AuditEntry[]>("/api/audit", 10000);
  return (
    <Card>
      <CardHeader title="Audit log" meta="every agent action" />
      <CardBody>
        {error ? <Empty>{error}</Empty> : (data ?? []).length === 0 ? <Empty>No keyed activity yet.</Empty> : (
          <ul className="max-h-72 space-y-1 overflow-y-auto font-mono text-[11px]">
            {data!.map((a) => (
              <li key={a.id} className="flex gap-2">
                <span className="shrink-0 text-dim">{new Date(a.at).toLocaleString()}</span>
                <span className="shrink-0 text-accent">{a.keyName}</span>
                <span className="min-w-0 flex-1 truncate">{a.action}</span>
                <span className={a.status === "ok" || a.status === "OK" ? "text-good" : "text-bad"}>{a.status}</span>
              </li>
            ))}
          </ul>
        )}
      </CardBody>
    </Card>
  );
}
