import { useCallback, useEffect, useRef, useState } from "react";

// Live video ladder: WebRTC (WHEP, sub-second) → HLS (Safari/iOS) →
// snapshot stills (camera only). The UI badges which tier is live.

export type Tier = "connecting" | "webrtc" | "hls" | "snapshot" | "nosignal";

async function whep(url: string, video: HTMLVideoElement, signal: AbortSignal): Promise<RTCPeerConnection> {
  const pc = new RTCPeerConnection();
  pc.addTransceiver("video", { direction: "recvonly" });
  pc.addTransceiver("audio", { direction: "recvonly" });
  pc.ontrack = (ev) => {
    if (video.srcObject !== ev.streams[0]) video.srcObject = ev.streams[0];
  };
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  await new Promise<void>((res) => {
    if (pc.iceGatheringState === "complete") return res();
    const t = setTimeout(res, 1500);
    pc.onicegatheringstatechange = () => {
      if (pc.iceGatheringState === "complete") {
        clearTimeout(t);
        res();
      }
    };
  });
  const r = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/sdp" },
    body: pc.localDescription!.sdp,
    signal,
  });
  if (!r.ok) {
    pc.close();
    throw new Error(`WHEP ${r.status}`);
  }
  await pc.setRemoteDescription({ type: "answer", sdp: await r.text() });
  // Wait for media to actually flow.
  await new Promise<void>((res, rej) => {
    const t = setTimeout(() => rej(new Error("no media")), 5000);
    video.onloadeddata = () => {
      clearTimeout(t);
      res();
    };
  });
  return pc;
}

async function playHls(url: string, video: HTMLVideoElement): Promise<() => void> {
  // hls.js loads only when this tier is actually needed (WebRTC and
  // native-HLS Safari never pay for it).
  const native = video.canPlayType("application/vnd.apple.mpegurl") !== "";
  const Hls = native ? null : (await import("hls.js")).default;
  return new Promise((res, rej) => {
    const timeout = setTimeout(() => rej(new Error("hls timeout")), 8000);
    if (native) {
      video.src = url;
      video.onloadeddata = () => {
        clearTimeout(timeout);
        res(() => {
          video.removeAttribute("src");
          video.load();
        });
      };
      video.onerror = () => {
        clearTimeout(timeout);
        rej(new Error("hls error"));
      };
      return;
    }
    if (!Hls || !Hls.isSupported()) {
      clearTimeout(timeout);
      return rej(new Error("no hls"));
    }
    const h = new Hls({ lowLatencyMode: true, liveSyncDurationCount: 2 });
    h.on(Hls.Events.FRAG_LOADED, () => {
      clearTimeout(timeout);
      res(() => h.destroy());
    });
    h.on(Hls.Events.ERROR, (_e, d) => {
      if (d.fatal) {
        clearTimeout(timeout);
        h.destroy();
        rej(new Error("hls fatal"));
      }
    });
    h.loadSource(url);
    h.attachMedia(video);
  });
}

export function useStream(opts: { whep?: string; hls?: string; snapshot?: string; snapshotMs?: number }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [tier, setTier] = useState<Tier>("connecting");
  const [snapUrl, setSnapUrl] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const retry = useCallback(() => setAttempt((n) => n + 1), []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || (!opts.whep && !opts.hls && !opts.snapshot)) return;
    let cancelled = false;
    const ac = new AbortController();
    let cleanup: (() => void) | null = null;
    let snapTimer: number | undefined;
    setTier("connecting");
    (async () => {
      if (opts.whep) {
        try {
          const pc = await whep(opts.whep, video, ac.signal);
          if (cancelled) return pc.close();
          cleanup = () => {
            pc.close();
            video.srcObject = null;
          };
          video.play().catch(() => {});
          setTier("webrtc");
          pc.onconnectionstatechange = () => {
            if (pc.connectionState === "failed" || pc.connectionState === "disconnected") retry();
          };
          return;
        } catch {
          /* next tier */
        }
      }
      if (cancelled) return;
      if (opts.hls) {
        try {
          const stop = await playHls(opts.hls, video);
          if (cancelled) return stop();
          cleanup = stop;
          video.play().catch(() => {});
          setTier("hls");
          return;
        } catch {
          /* next tier */
        }
      }
      if (cancelled) return;
      if (opts.snapshot) {
        const tick = () => setSnapUrl(`${opts.snapshot}?t=${Date.now()}`);
        tick();
        snapTimer = window.setInterval(tick, opts.snapshotMs ?? 900);
        setTier("snapshot");
        return;
      }
      setTier("nosignal");
    })();
    return () => {
      cancelled = true;
      ac.abort();
      cleanup?.();
      clearInterval(snapTimer);
      setSnapUrl(null);
    };
  }, [opts.whep, opts.hls, opts.snapshot, opts.snapshotMs, attempt, retry]);

  return { videoRef, tier, snapUrl, retry };
}
