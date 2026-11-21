import { useState } from "react";
import { Loader2 } from "lucide-react";
import { cn } from "@/lib/cn";
import { Frame } from "../widgets";
import { ConnChip, label, listOf, stateClass, str, useAddonAction, type ShapeProps } from "./common";

// kind "input": a text box, optional chips and a send button.
// bind: action, param (where the text goes), placeholder?, choices? (state key
// or static [{value,label}]), choiceParam?, button?
// With choiceParam the chips pick a value sent along with the text (ESV /
// NLT); without it a chip sends its value as the text right away (quick
// replies).

export function InputShape({ def, state, conn, preview, editing }: ShapeProps) {
  const b = def.bind ?? {};
  const action = str(b.action), param = str(b.param, "text"), choiceParam = str(b.choiceParam);
  const choices = listOf(state, b, "choices").map((c) => ({ value: str(c.value), label: str(c.label) || str(c.value) })).filter((c) => c.value);
  const [text, setText] = useState("");
  const [pick, setPick] = useState<string | null>(null);
  const chosen = pick ?? choices[0]?.value ?? "";
  const { run, busy } = useAddonAction(conn, preview);
  const off = !action || (!conn && !preview) || editing;
  const send = async (value: string) => {
    const v = value.trim();
    if (!v || busy) return;
    const params: Record<string, unknown> = { [param]: v };
    if (choiceParam && chosen) params[choiceParam] = chosen;
    if (await run(action, params, `${def.label}: ${v}${choiceParam && chosen ? ` (${chosen})` : ""}`, "send") && value === text) setText("");
  };
  const btn = label(state, b, "button") || "Send";
  return (
    <Frame title={def.label} right={<ConnChip conn={conn} />}>
      <form className="flex h-full flex-col gap-2 overflow-y-auto" onSubmit={(e) => { e.preventDefault(); send(text); }}>
        <input value={text} onChange={(e) => setText(e.target.value)} disabled={off || !!busy} maxLength={500}
          placeholder={label(state, b, "placeholder") || "Type here…"} aria-label={def.label}
          className="recess h-10 w-full shrink-0 rounded-[10px] border px-3 text-sm text-fg transition-colors placeholder:text-dim focus:border-accent focus:outline-none disabled:opacity-60" />
        <div className="flex min-h-0 items-center gap-2">
          {choices.length > 0 && (
            // Chips wrap; the widget grows to fit them.
            <div className="flex min-w-0 flex-1 flex-wrap gap-1.5">
              {choices.map((c) => {
                const on = !!choiceParam && c.value === chosen;
                return (
                  <button key={c.value} type="button" disabled={off || !!busy} title={c.label}
                    onClick={() => (choiceParam ? setPick(c.value) : send(c.value))}
                    className={cn("h-8 max-w-[160px] truncate rounded-full border px-3 text-xs font-bold tracking-[0.02em] transition active:scale-[0.97] disabled:active:scale-100",
                      on ? "btn-sel" : "btn-raised text-muted hover:text-fg")}>
                    {c.label}
                  </button>
                );
              })}
            </div>
          )}
          <button type="submit" disabled={off || !!busy || !text.trim()}
            className={cn("ml-auto flex h-10 shrink-0 items-center gap-1.5 rounded-[10px] border px-4 text-[13.5px] font-bold transition active:scale-[0.98] disabled:active:scale-100",
              // the widget's primary action: amber; flat and dim until there's text to send
              stateClass("accent", false, off || !!busy || !text.trim()))}>
            {busy && <Loader2 size={14} className="animate-spin" />}{btn}
          </button>
        </div>
      </form>
    </Frame>
  );
}
