import { useEffect, useRef, useState } from "react";
import { ExternalLink, Loader2 } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import type { ActionResult, Field } from "@/lib/addons";
import { Button } from "@/components/ui";

// An add-on's action field: a button that runs a connection action on the
// saved connection (a sign-in, a pairing) and shows what it answers: a
// code to type somewhere, a page to open, then the result. While the
// add-on says poll, it is asked again every few seconds.

const POLL_MS = 3000;

export function ActionField({ f, connId }: { f: Field; connId?: string }) {
  const [res, setRes] = useState<ActionResult | null>(null);
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);
  const timer = useRef<number | undefined>(undefined);

  const call = async (params: Record<string, unknown>) => {
    if (!connId || !f.action) return;
    try {
      const out = await api<{ result?: ActionResult }>(`/api/connections/${connId}/actions/${f.action}`, "POST", params);
      if (!alive.current) return;
      const r = out.result ?? {};
      setRes(r);
      window.clearTimeout(timer.current);
      if (r.poll) timer.current = window.setTimeout(() => call({ poll: true }), POLL_MS);
    } catch (e) {
      if (alive.current && !params.status) setRes({ error: (e as Error).message });
    }
  };

  useEffect(() => {
    alive.current = true;
    if (connId) call({ status: true });
    return () => { alive.current = false; window.clearTimeout(timer.current); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId, f.action]);

  const press = async () => {
    setBusy(true);
    try { await call({}); } finally { setBusy(false); }
  };

  return (
    <div className="rounded-xl border border-line bg-panel-2 p-3 shadow-[var(--shadow-hi)]">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1 text-[13px] font-bold text-fg">{f.label}</span>
        {connId && (
          <Button size="sm" onClick={press} disabled={busy || (res?.poll ?? false)}>
            {busy && <Loader2 size={13} className="animate-spin" />}
            {res?.button || f.placeholder || f.label}
          </Button>
        )}
      </div>
      {f.help && <p className="mt-1 text-[11.5px] leading-[1.45] text-muted">{f.help}</p>}
      {!connId && <p className="mt-1.5 text-[11.5px] text-dim">Save the connection first, then come back here.</p>}
      {res && (res.text || res.code || res.url || res.error) && (
        <div className="mt-2 space-y-2">
          {res.text && <p className={cn("text-[12.5px] leading-[1.45]", res.ok ? "text-good" : "text-muted")}>{res.ok ? "✓ " : ""}{res.text}</p>}
          {res.code && (
            <div className="recess rounded-lg border px-3 py-2">
              <code className="select-all break-all font-mono text-[22px] font-bold tracking-[0.15em] text-accent">{res.code}</code>
            </div>
          )}
          {res.url && /^https?:\/\//.test(res.url) && (
            <a href={res.url} target="_blank" rel="noopener noreferrer"
              className="btn-raised inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs font-semibold hover:brightness-125">
              Open {res.url.replace(/^https?:\/\//, "").replace(/\/.*$/, "")} <ExternalLink size={12} />
            </a>
          )}
          {res.poll && <p className="flex items-center gap-1.5 text-[11.5px] text-dim"><Loader2 size={12} className="animate-spin" /> Waiting for you…</p>}
          {res.error && <p className="text-[12.5px] text-bad">{res.error}</p>}
        </div>
      )}
    </div>
  );
}
