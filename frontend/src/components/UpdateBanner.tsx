import { useEffect, useState } from "react";
import { Download, RotateCcw, CheckCircle2, ShieldAlert, Loader2 } from "lucide-react";
import { api, type UpdateStatus } from "@/lib/api";
import { usePoll } from "@/lib/hooks";
import { Button } from "@/components/ui";

// Shows only when there's something to say: a verified update ready, an
// update in progress, or how the last one went.
export function UpdateBanner() {
  const { data: st, reload } = usePoll<UpdateStatus>("/api/update/status", 30000);
  const [busy, setBusy] = useState(false);
  const [applying, setApplying] = useState<string | null>(null);
  const [msg, setMsg] = useState("");

  useEffect(() => {
    if (!applying) return;
    const started = Date.now();
    const t = setInterval(async () => {
      try {
        const v = await api<{ version: string }>("/api/version");
        if (v.version === applying) {
          clearInterval(t);
          location.reload();
        } else if (Date.now() - started > 20000) {
          // back on the old version: rolled back
          const s = await api<UpdateStatus>("/api/update/status");
          if (s.state === "rolled_back" || s.state === "failed") {
            clearInterval(t);
            setApplying(null);
            reload();
          }
        }
      } catch {
        /* restarting */
      }
      if (Date.now() - started > 120000) {
        clearInterval(t);
        setApplying(null);
        setMsg("The controller hasn't come back yet. Reload this page in a moment.");
      }
    }, 1500);
    return () => clearInterval(t);
  }, [applying, reload]);

  if (applying) {
    return (
      <div className="fixed inset-0 z-[60] flex items-center justify-center bg-bg/95 p-6">
        <div className="max-w-sm text-center">
          <Loader2 className="mx-auto mb-4 animate-spin text-accent" size={36} />
          <div className="text-lg font-semibold">Updating to {applying}…</div>
          <p className="mt-2 text-sm text-muted">The controller restarts and checks itself. If anything is wrong it goes back to the previous version on its own. This page reloads when it's back.</p>
        </div>
      </div>
    );
  }
  if (!st) return null;
  const dismiss = async () => {
    await api("/api/update/dismiss", "POST").catch(() => {});
    reload();
  };
  const apply = async () => {
    setBusy(true);
    setMsg("");
    try {
      const r = await api<{ version: string }>("/api/update/apply", "POST");
      setApplying(r.version);
    } catch (e) {
      setMsg((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const bar = (icon: React.ReactNode, text: React.ReactNode, actions?: React.ReactNode, tone = "border-accent/40 bg-accent/10") => (
    <div className={`border-b ${tone}`}>
      <div className="mx-auto flex max-w-[1600px] flex-wrap items-center gap-3 px-3 py-2.5 text-sm sm:px-5">
        {icon}
        <div className="min-w-0 flex-1">{text}{msg && <span className="ml-2 text-bad">{msg}</span>}</div>
        {actions}
      </div>
    </div>
  );
  switch (st.state) {
    case "available":
      return bar(<Download size={18} className="text-accent" />,
        <span><b>Update {st.version} is ready</b> <span className="text-muted">— verified and signed. Running {st.current}.</span></span>,
        <Button variant="accent" size="sm" onClick={apply} disabled={busy}>{busy ? "Starting…" : "Update now"}</Button>);
    case "applied":
      return bar(<CheckCircle2 size={18} className="text-good" />, <span><b>Updated to {st.version}.</b></span>,
        <Button size="sm" variant="ghost" onClick={dismiss}>Dismiss</Button>, "border-good/30 bg-good/10");
    case "rolled_back":
      return bar(<RotateCcw size={18} className="text-warn" />,
        <span><b>Update {st.version} was rolled back</b> <span className="text-muted">— {st.reason}. Still on {st.current}.</span></span>,
        <Button size="sm" variant="ghost" onClick={dismiss}>Dismiss</Button>, "border-warn/30 bg-warn/10");
    case "failed":
      return bar(<ShieldAlert size={18} className="text-bad" />, <span><b>Update failed:</b> <span className="text-muted">{st.reason}</span></span>,
        <Button size="sm" variant="ghost" onClick={dismiss}>Dismiss</Button>, "border-pgm/30 bg-pgm/10");
    case "invalid":
      return bar(<ShieldAlert size={18} className="text-bad" />,
        <span><b>A release failed its safety check and was ignored.</b> <span className="text-muted">{st.reason}</span></span>,
        undefined, "border-pgm/30 bg-pgm/10");
  }
  return null;
}
