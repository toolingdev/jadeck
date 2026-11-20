import { useCallback, useEffect, useRef, useState } from "react";

// Live video ladder: WebRTC (WHEP, sub-second) → HLS (Safari/iOS) →
// snapshot stills (camera only). The UI badges which tier is live.

export type Tier = "connecting" | "webrtc" | "hls" | "snapshot" | "nosignal" | "paused";

// ---------------------------------------------------- shared WebRTC feeds
// Several widgets on one screen showing the same feed share one WebRTC
// connection: one stream over the Wi-Fi, decoded once per <video>.

type Feed = {
  url: string; refs: number; pc: RTCPeerConnection | null; ready: Promise<MediaStream>;
  closeTimer?: number; failed: Set<() => void>; dead: boolean;
};
const feeds = new Map<string, Feed>();

function openFeed(url: string): Feed {
  const f: Feed = { url, refs: 0, pc: null, failed: new Set(), dead: false, ready: Promise.resolve(new MediaStream()) };
  f.ready = (async () => {
    const pc = new RTCPeerConnection();
    f.pc = pc;
    pc.addTransceiver("video", { direction: "recvonly" });
    pc.addTransceiver("audio", { direction: "recvonly" });
    const gotTrack = new Promise<MediaStream>((res) => {
      pc.ontrack = (ev) => res(ev.streams[0] ?? new MediaStream([ev.track]));
    });
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === "failed" || pc.connectionState === "disconnected") {
        f.dead = true;
        if (feeds.get(url) === f) feeds.delete(url);
        pc.close();
        f.failed.forEach((cb) => cb());
      }
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
    const r = await fetch(url, { method: "POST", headers: { "Content-Type": "application/sdp" }, body: pc.localDescription!.sdp });
    if (!r.ok) throw new Error(`WHEP ${r.status}`);
    await pc.setRemoteDescription({ type: "answer", sdp: await r.text() });
    return await Promise.race([gotTrack, new Promise<MediaStream>((_, rej) => setTimeout(() => rej(new Error("no media")), 5000))]);
  })();
  f.ready.catch(() => {
    f.dead = true;
    f.pc?.close();
    if (feeds.get(url) === f) feeds.delete(url);
  });
  return f;
}

/** Join (or open) the shared WebRTC feed for url. */
async function acquireWhep(url: string, onFail: () => void): Promise<{ stream: MediaStream; release: () => void }> {
  let f = feeds.get(url);
  if (!f || f.dead) {
    f = openFeed(url);
    feeds.set(url, f);
  }
  f.refs++;
  clearTimeout(f.closeTimer);
  f.failed.add(onFail);
  const feed = f;
  let released = false;
  const release = () => {
    if (released) return;
    released = true;
    feed.failed.delete(onFail);
    feed.refs--;
    if (feed.refs <= 0) {
      // A short grace so a widget re-rendering doesn't tear the feed down.
      feed.closeTimer = window.setTimeout(() => {
        if (feed.refs > 0) return;
        feed.pc?.close();
        if (feeds.get(url) === feed) feeds.delete(url);
      }, 3000);
    }
  };
  try {
    return { stream: await feed.ready, release };
  } catch (e) {
    release();
    throw e;
  }
}

/** How many shared WebRTC feeds this screen has open (for diagnostics). */
export function openFeeds() {
  return [...feeds.values()].filter((f) => !f.dead && f.refs > 0).map((f) => ({ url: f.url, widgets: f.refs }));
}

function loaded(video: HTMLVideoElement, ms: number) {
  return new Promise<void>((res, rej) => {
    if (video.readyState >= 2) return res();
    const t = setTimeout(() => rej(new Error("no media")), ms);
    video.onloadeddata = () => {
      clearTimeout(t);
      res();
    };
  });
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

export function useStream(opts: { whep?: string; hls?: string; snapshot?: string; snapshotMs?: number; active?: boolean }) {
  const active = opts.active ?? true;
  const videoRef = useRef<HTMLVideoElement>(null);
  const [tier, setTier] = useState<Tier>("connecting");
  const [snapUrl, setSnapUrl] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const retry = useCallback(() => setAttempt((n) => n + 1), []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || (!opts.whep && !opts.hls && !opts.snapshot)) return;
    if (!active) {
      setTier("paused"); // nobody can see it: no stream over the Wi-Fi
      return;
    }
    let cancelled = false;
    const ac = new AbortController();
    let cleanup: (() => void) | null = null;
    let snapTimer: number | undefined;
    setTier("connecting");
    (async () => {
      if (opts.whep) {
        let rel: (() => void) | null = null;
        try {
          const { stream, release } = await acquireWhep(opts.whep, () => !cancelled && retry());
          rel = release;
          if (cancelled) return release();
          video.srcObject = stream;
          video.play().catch(() => {});
          await loaded(video, 5000);
          if (cancelled) return release();
          cleanup = () => {
            release();
            video.srcObject = null;
          };
          setTier("webrtc");
          return;
        } catch {
          rel?.();
          video.srcObject = null;
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
  }, [opts.whep, opts.hls, opts.snapshot, opts.snapshotMs, attempt, retry, active]);

  return { videoRef, tier, snapUrl, retry };
}
