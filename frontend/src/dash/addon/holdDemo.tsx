import { useState } from "react";
import { HoldButton } from "./hold";

// Settings → Add-ons: a place to try "hold to confirm" (design/WIDGETS-V2.md).
// Nothing is sent; the buttons just flip a pretend state.

export function HoldDemo() {
  const [streaming, setStreaming] = useState(true);
  const [recording, setRecording] = useState(false);
  return (
    <section className="card-surface space-y-2 rounded-[var(--radius-card)] border border-line p-3">
      <div className="flex items-baseline gap-2">
        <h3 className="text-[13px] font-bold">Try it: hold to confirm</h3>
        <span className="smallcaps ml-auto">preview · sends nothing</span>
      </div>
      <p className="text-[11px] text-dim">Add-on buttons with <code className="font-mono">hold</code> must be held. Let go early and nothing happens. Space / Enter work too.</p>
      <div className="grid h-[52px] grid-cols-2 gap-2">
        <HoldButton seconds={3} tone="pgm" label={streaming ? "Stop stream" : "Start stream"} sub={streaming ? "LIVE" : "OFF AIR"}
          className="btn-raised border-pgm/55 hover:brightness-125" onFire={() => setStreaming((v) => !v)} />
        <HoldButton seconds={3} tone="pgm" label={recording ? "Stop recording" : "Record"} sub={recording ? "REC" : "IDLE"}
          className="btn-raised border-pgm/55 hover:brightness-125" onFire={() => setRecording((v) => !v)} />
      </div>
    </section>
  );
}
