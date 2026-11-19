import { useCallback, useEffect, useState } from "react";
import { type StreamConfig } from "@/lib/api";
import { usePoll, useLocal } from "@/lib/hooks";
import { useControl, setDrive } from "@/lib/control";
import { StreamView } from "@/components/StreamView";
import { DirPad, Joystick, ZoomStop, SpeedControl, FocusControl, FINE_SPEED } from "@/components/Drive";
import { PresetGrid } from "@/components/Presets";
import { ImageSettings } from "@/components/ImageSettings";
import { WbPickOverlay } from "@/components/WbPick";
import { Card, CardBody, CardHeader, Segmented, Button } from "@/components/ui";
import { useLog } from "@/lib/log";

export function useStreamConfig() {
  return usePoll<StreamConfig>("/api/config", 0).data;
}

export function CameraPage() {
  const cfg = useStreamConfig();
  const { status } = useControl();
  const [quality, setQuality] = useLocal<"hd" | "sd">("ptz.quality", "hd");
  const [mode, setMode] = useLocal<"buttons" | "joystick">("ptz.mode", "buttons");
  const [speed, setSpeed] = useLocal("ptz.speed", 40);
  const [fine, setFine] = useLocal("ptz.fine", false);
  const top = fine ? Math.min(FINE_SPEED, speed) : speed;
  // The saved speed / Fine apply from the first press, not only after the slider moves.
  useEffect(() => setDrive({ speed: top }), [top, mode]);
  const [wb, setWb] = useState(false);
  const [media, setMedia] = useState<HTMLVideoElement | HTMLImageElement | null>(null);
  const onMedia = useCallback((m: HTMLVideoElement | HTMLImageElement | null) => setMedia(m), []);
  const { log } = useLog();

  const startWb = () => {
    setWb(true);
    window.scrollTo({ top: 0, behavior: "smooth" });
  };
  const snapNow = async () => {
    try {
      const r = await fetch(cfg?.snapshot ?? "/api/snapshot");
      if (!r.ok) throw new Error("snapshot unavailable");
      const url = URL.createObjectURL(await r.blob());
      window.open(url, "_blank");
    } catch (e) {
      log("Camera", (e as Error).message, false);
    }
  };

  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_380px] landscape:max-lg:grid-cols-[minmax(0,1fr)_320px]">
      {/* Stage */}
      <div className="space-y-3 lg:sticky lg:top-[68px] lg:self-start landscape:max-lg:sticky landscape:max-lg:top-[60px] landscape:max-lg:self-start">
        <StreamView
          key={quality}
          whep={cfg ? (quality === "hd" ? cfg.whep : cfg.whepSd) : undefined}
          hls={cfg ? (quality === "hd" ? cfg.hls : cfg.hlsSd) : undefined}
          snapshot={cfg?.snapshot}
          onMedia={onMedia}
          overlay={wb ? <WbPickOverlay media={media} onClose={() => setWb(false)} /> : undefined}
        />
        <div className="flex flex-wrap items-center gap-2">
          <Segmented value={quality} onChange={setQuality} options={[{ value: "hd", label: "HD" }, { value: "sd", label: "SD" }]} />
          <Button size="sm" variant="ghost" onClick={snapNow}>Snapshot</Button>
          {cfg && (quality === "hd" ? cfg.whep : cfg.whepSd) && <a className="text-xs text-muted underline-offset-2 hover:text-fg hover:underline" href={(quality === "hd" ? cfg.whep : cfg.whepSd)!.replace(/\/whep$/, "/")} target="_blank" rel="noreferrer">Open player</a>}
          {status?.error && <span className="ml-auto truncate text-xs text-bad">{status.error}</span>}
        </div>
        <div className="max-lg:hidden landscape:max-lg:hidden">
          <PresetGrid />
        </div>
      </div>

      {/* Control rail */}
      <div className="space-y-3">
        <Card>
          <CardHeader title="Move">
            <Segmented value={mode} onChange={setMode} options={[{ value: "buttons", label: "Buttons" }, { value: "joystick", label: "Joystick" }]} />
          </CardHeader>
          <CardBody className="space-y-4">
            <div className="flex items-center justify-center gap-4">
              {mode === "buttons" ? <DirPad /> : <Joystick speed={top} />}
              <ZoomStop home={mode === "joystick"} />
            </div>
            <SpeedControl speed={speed} onChange={setSpeed} fine={fine} onFine={setFine} label={mode === "joystick" ? "Top speed" : "Speed"} />
          </CardBody>
        </Card>
        <Card>
          <CardHeader title="Focus" meta={status?.focusAuto == null ? "" : status.focusAuto ? "auto" : "manual"} />
          <CardBody><FocusControl auto={status?.focusAuto ?? null} /></CardBody>
        </Card>
        <div className="lg:hidden landscape:max-lg:block landscape:lg:hidden">
          <PresetGrid />
        </div>
        <ImageSettings onWbPick={startWb} />
      </div>
    </div>
  );
}
