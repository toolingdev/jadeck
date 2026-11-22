import { useState } from "react";
import { Check, ChevronDown, ChevronUp, Copy, ExternalLink } from "lucide-react";
import { cn } from "@/lib/cn";
import type { Guide } from "@/lib/addons";

// "How to set up": a connection type's own steps (from its add-on), shown
// above the connection's fields. Open for a new connection; folded once
// it works, with Show to bring it back.

export function SetupGuide({ guide, open, onToggle }: { guide: Guide; open: boolean; onToggle: () => void }) {
  const mins = guide.minutes ? ` · ${guide.minutes} minute${guide.minutes === 1 ? "" : "s"}` : "";
  return (
    <section className="rounded-xl border border-line bg-panel-2 shadow-[var(--shadow-hi)]">
      <button type="button" onClick={onToggle} aria-expanded={open}
        className="flex w-full items-center gap-2 px-3 py-2.5 text-left">
        <span className="min-w-0 flex-1 truncate text-[13px] font-bold text-fg">How to set up{mins}</span>
        <span className="flex shrink-0 items-center gap-1 text-xs text-muted">
          {open ? <>Hide <ChevronUp size={14} /></> : <>Show <ChevronDown size={14} /></>}
        </span>
      </button>
      {open && (
        <div className="space-y-3 px-3 pb-3">
          <ol className="space-y-2.5">
            {guide.steps.map((s, i) => (
              <li key={i} className="flex gap-2.5">
                <span className="recess mt-px flex h-5 w-5 shrink-0 items-center justify-center rounded-full border font-mono text-[11px] font-bold text-muted">{i + 1}</span>
                <div className="min-w-0 flex-1 space-y-1.5">
                  <p className="text-[12.5px] leading-[1.45] text-muted">{s.text}</p>
                  {s.code && <CopyCode text={s.code} />}
                  {s.link && /^https:\/\//.test(s.link.url) && (
                    <a href={s.link.url} target="_blank" rel="noopener noreferrer"
                      className="btn-raised inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs font-semibold hover:brightness-125">
                      {s.link.label} <ExternalLink size={12} />
                    </a>
                  )}
                </div>
              </li>
            ))}
          </ol>
          {guide.note && (
            <p className="rounded-lg bg-raise/60 px-2.5 py-2 text-[11.5px] leading-[1.45] text-muted">
              <span className="font-semibold text-fg">Good to know: </span>{guide.note}
            </p>
          )}
        </div>
      )}
    </section>
  );
}

// Something to type or paste: monospaced, selectable in one tap, with a
// Copy button where the browser allows it (secure pages only).
function CopyCode({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  const canCopy = typeof navigator !== "undefined" && !!navigator.clipboard && window.isSecureContext;
  const copy = async () => {
    try { await navigator.clipboard.writeText(text); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* select-all still works */ }
  };
  return (
    <div className="recess flex min-w-0 items-center gap-2 rounded-lg border px-2.5 py-1.5">
      <code className="min-w-0 flex-1 select-all break-all font-mono text-[12px] text-fg">{text}</code>
      {canCopy && (
        <button type="button" onClick={copy} aria-label="Copy" className={cn("shrink-0 text-muted hover:text-fg", done && "text-good")}>
          {done ? <Check size={14} /> : <Copy size={14} />}
        </button>
      )}
    </div>
  );
}
