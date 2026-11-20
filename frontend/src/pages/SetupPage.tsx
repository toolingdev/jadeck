import { useState } from "react";
import { ArrowLeft, ArrowRight, Camera, Check, PartyPopper, Plus } from "lucide-react";
import { api, type SystemInfo } from "@/lib/api";
import { usePoll } from "@/lib/hooks";
import { cn } from "@/lib/cn";
import { CameraForm, useCameras } from "@/components/CameraSetup";
import { ConnectionsSection } from "@/components/Connections";
import { DashboardSettings } from "@/components/DashboardSettings";
import { Button, Card, CardBody, Dot } from "@/components/ui";

const steps = ["Welcome", "Camera", "Switcher & slides", "Sound", "Dashboard", "Done"] as const;

/** First run: opens by itself until it is finished or skipped. */
export function SetupPage({ onFinish }: { onFinish: () => void }) {
  const [step, setStep] = useState(0);
  const { data: cams, reload } = useCameras();
  const [adding, setAdding] = useState(false);
  const finish = async () => {
    await api("/api/setup", "POST", { done: true }).catch(() => {});
    onFinish();
  };
  const next = () => setStep((s) => Math.min(s + 1, steps.length - 1));
  const back = () => setStep((s) => Math.max(s - 1, 0));
  const haveCam = (cams?.length ?? 0) > 0;

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <Stepper step={step} onGo={(i) => i < step && setStep(i)} />
      <Card>
        <CardBody className="space-y-4 pt-5">
          {step === 0 && (
            <div className="space-y-3">
              <h1 className="text-2xl font-bold">Set up jadeck</h1>
              <p className="text-sm text-muted">A few minutes: find your camera, tell it about your video switcher, slides and sound board (all optional), pick a dashboard. Every answer can be changed later in Settings, nothing needs a file or a terminal.</p>
              <ul className="space-y-1 text-sm text-muted">
                <li>• Have the camera powered on and on the same network as this PC.</li>
                <li>• Newer camera firmware needs the camera's login (often admin).</li>
              </ul>
            </div>
          )}

          {step === 1 && (
            <div className="space-y-3">
              <h2 className="text-xl font-bold">{haveCam ? "Your cameras" : "Let's find your camera"}</h2>
              {haveCam && !adding ? (
                <>
                  <div className="space-y-2">
                    {cams!.map((c) => (
                      <div key={c.id} className="flex items-center gap-3 rounded-xl border border-line bg-panel-2 p-3">
                        <Camera size={18} className="text-accent" />
                        <div className="min-w-0 flex-1">
                          <div className="text-sm font-semibold">{c.name}{c.main && <span className="ml-2 text-xs text-accent">main</span>}</div>
                          <div className="text-xs text-muted">{c.host}</div>
                        </div>
                        <span className={cn("flex items-center gap-1.5 text-xs", c.status?.connected ? "text-good" : "text-bad")}>
                          <Dot on={!!c.status?.connected} /> {c.status?.connected ? "connected" : "not answering"}
                        </span>
                      </div>
                    ))}
                  </div>
                  <Button variant="ghost" onClick={() => setAdding(true)}><Plus size={15} /> Add another camera</Button>
                </>
              ) : (
                <>
                  <p className="text-sm text-muted">Pick one that was found, or type its IP address. Test shows what it sees before you add it.</p>
                  <CameraForm saveLabel="Add camera" defaultName={haveCam ? "" : "Sanctuary"}
                    onCancel={haveCam ? () => setAdding(false) : undefined}
                    onSaved={() => { setAdding(false); reload(); }} />
                </>
              )}
            </div>
          )}

          {step === 2 && (
            <div className="space-y-3">
              <h2 className="text-xl font-bold">Video switcher and slides</h2>
              <p className="text-sm text-muted">vMix, Proclaim, and others. Skip if you don't have them. Remember Save & apply.</p>
              <ConnectionsSection only={["switcher", "slides"]} compact />
            </div>
          )}
          {step === 3 && (
            <div className="space-y-3">
              <h2 className="text-xl font-bold">Sound board</h2>
              <p className="text-sm text-muted">Mute and unmute channels from a phone (Behringer WING and others). Skip if you don't need it.</p>
              <ConnectionsSection only={["mixer"]} compact />
            </div>
          )}
          {step === 4 && (
            <div className="space-y-3">
              <h2 className="text-xl font-bold">Dashboards</h2>
              <p className="text-sm text-muted">There's a ready-made one for each kind of screen. Pick the defaults, or change them later and build your own.</p>
              <DashboardSettings />
            </div>
          )}
          {step === 5 && <Done />}

          <div className="flex items-center gap-2 border-t border-line pt-4">
            {step < steps.length - 1 && <button className="text-xs text-dim hover:text-fg" onClick={finish}>Skip setup</button>}
            <div className="ml-auto flex gap-2">
              {step > 0 && <Button variant="ghost" onClick={back}><ArrowLeft size={15} /> Back</Button>}
              {step < steps.length - 1 ? (
                <Button variant="accent" onClick={next} disabled={step === 1 && adding}>
                  {step === 0 ? "Start" : step === 1 && !haveCam ? "Skip camera for now" : "Next"} <ArrowRight size={15} />
                </Button>
              ) : (
                <Button variant="accent" onClick={finish}><Check size={15} /> Open the controller</Button>
              )}
            </div>
          </div>
        </CardBody>
      </Card>
    </div>
  );
}

function Stepper({ step, onGo }: { step: number; onGo: (i: number) => void }) {
  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
        {steps.map((s, i) => (
          <span key={s} className="flex items-center gap-2">
            {i > 0 && <span className="text-dim">—</span>}
            <button onClick={() => onGo(i)} disabled={i >= step}
              className={cn(i === step ? "font-semibold text-fg" : i < step ? "text-muted hover:text-fg" : "text-dim")}>
              {i < step ? "✓" : i === step ? "●" : "○"} {s}
            </button>
          </span>
        ))}
      </div>
      <div className="mt-2 h-1 rounded-full bg-raise">
        <div className="h-1 rounded-full bg-accent transition-all" style={{ width: `${(step / (steps.length - 1)) * 100}%` }} />
      </div>
    </div>
  );
}

function Done() {
  const { data } = usePoll<SystemInfo>("/api/system", 0);
  const url = data?.urls.find((u) => !u.includes(".local")) ?? data?.urls[0];
  return (
    <div className="flex flex-wrap items-center gap-6">
      <div className="min-w-0 flex-1 space-y-2">
        <h2 className="flex items-center gap-2 text-xl font-bold"><PartyPopper size={22} className="text-accent" /> Ready</h2>
        <p className="text-sm text-muted">Open it on any phone or tablet on the venue Wi-Fi. No login needed on the venue network.</p>
        {data?.urls.slice(0, 3).map((u) => <div key={u} className="font-mono text-sm">{u}</div>)}
        <p className="text-xs text-dim">Tip: on a phone, use "Add to Home Screen" so it opens like an app.</p>
      </div>
      {url && <img src={`/api/system/qr?text=${encodeURIComponent(url)}`} alt={url} className="w-44 rounded-xl bg-white p-2 [image-rendering:pixelated]" />}
    </div>
  );
}
