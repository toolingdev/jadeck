// Thin fetch wrapper. Errors carry the server's "detail" text so the UI
// can show exactly why something didn't happen. Camera calls name their
// camera with ?cam= (see withCam); without it the server uses the main one.

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function api<T = unknown>(path: string, method = "GET", body?: unknown): Promise<T> {
  let r: Response;
  try {
    const headers: Record<string, string> = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    r = await fetch(path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, "the controller is not answering");
  }
  const text = await r.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (!r.ok) {
    const d = (data as { detail?: string } | null)?.detail;
    throw new ApiError(r.status, d || `HTTP ${r.status}`);
  }
  // Something changed (a camera renamed, a feed added…): every list on
  // screen reads again, so names are never stale.
  if (method !== "GET" && typeof window !== "undefined") window.dispatchEvent(new Event("ptz:changed"));
  return data as T;
}

// ------------------------------------------------------------- types

export type CamStatus = {
  connected: boolean;
  driving: boolean;
  power: boolean | null;
  pan: number | null;
  tilt: number | null;
  zoom: number | null;
  focusAuto: boolean | null;
  device: Record<string, string>;
  error: string;
};

export type Preset = { id: number; name: string; slot: number; sort: number; exposure: unknown };

export type StreamConfig = {
  camera: string; snapshot: string;
  whep?: string; hls?: string; whepSd?: string; hlsSd?: string;
  programWhep?: string; programHls?: string; confidenceWhep?: string; confidenceHls?: string;
  feeds?: { id: string; name: string; source: "push" | "pull"; whep?: string; hls?: string }[];
};

export type CameraInfo = {
  id: string; name: string; type: string; host: string; viscaPort: number; httpPort: number; rtspPort: number;
  streamHd: string; streamSd: string; user: string; video: boolean;
  hasPassword: boolean; main: boolean; status: CamStatus | null;
};
export type ProbeResult = {
  ok: boolean; message: string; control: boolean; controlError?: string; picture: boolean; pictureError?: string;
  login: "ok" | "none" | "needed" | "rejected" | "unknown"; model?: string; firmware?: string; snapshot?: string;
};
export type FoundCamera = { host: string; viscaPort: number; httpPort: number; model?: string; login: boolean; known: string };

export type VideoInfo = {
  settings: { managed: boolean; program: boolean; confidence: boolean; external: string };
  status: {
    managed: boolean; state: "off" | "missing" | "installing" | "starting" | "running" | "failed"; version: string;
    error?: string; installed?: string; canFetch: boolean; bundled: boolean; paths: { name: string; ready: boolean; readers: number }[] | null; log: string[] | null;
  };
  cameras: { id: string; name: string; video: boolean; hd: string; sd: string }[];
  programSrt: string; confidenceSrt: string;
};
export type SystemSettings = {
  port: number; name: string; trustLan: boolean; trustedCidrs: string[] | null;
  updateRepo: string; channel: "stable" | "beta"; autoUpdate: boolean;
};
export type SystemInfo = {
  system: SystemSettings; hasToken: boolean; portLocked: boolean; mdnsError: string; version: string;
  addresses: { ip: string; interface: string; kind: "lan" | "tailscale" }[]; urls: string[]; update: UpdateStatus;
};

export type FieldSpec = {
  name: string; key: string; group: string; kind: "int" | "enum" | "bool";
  min?: number; max?: number; values?: string[]; label: string;
};

export type Input = { number: number; key: string; title: string; type: string; state: string };
export type SwitcherStatus = {
  connected: boolean; simulated: boolean; driver: string; label: string; error?: string;
  inputs: Input[]; preview: number; active: number; streaming: boolean; recording: boolean;
  fadeToBlack: boolean; version: string; edition: string;
};
export type PresentationStatus = {
  available: boolean; simulated: boolean; driver: string; label: string; port: string; actions: string[];
  channel?: number; notes?: Record<string, number>;
};
export type MixerChannel = { slot: number; label: string; channel: number | null; muted: boolean | null };
export type MixerStatus = {
  configured: boolean; connected: boolean; simulated: boolean; driver: string; label: string;
  console: string | null; error?: string; channels: MixerChannel[];
};

export type Step = {
  type: "mixer_mute" | "switcher" | "slides" | "camera_preset" | "delay" | "addon";
  channel?: number; channelName?: string; muted?: boolean;
  action?: string; input?: number; durationMs?: number; preset?: string; ms?: number;
  addon?: string; connection?: string; params?: Record<string, unknown>; label?: string;
};
export type StepResult = { step: number; type: string; label: string; ok: boolean; detail?: string; error?: string; ms: number };
export type RunReport = {
  macroId: number; name: string; ok: boolean; failedStep: number | null; error: string | null;
  startedAt: number; finishedAt: number; steps: StepResult[];
};
export type Macro = {
  id: number; name: string; description: string; steps: Step[]; sort: number;
  lastRun: RunReport | null; createdAt: number; updatedAt: number;
};

export type UpdateStatus = {
  state: "none" | "checking" | "available" | "invalid" | "applying" | "applied" | "rolled_back" | "failed" | "disabled";
  current: string; version?: string; notes?: string; reason?: string; lastCheck?: string; repo?: string;
};

export type Scope = { id: string; name: string; description: string };
export type ApiKey = {
  id: number; name: string; prefix: string; scopes: string[]; enabled: boolean;
  createdAt: string; lastUsedAt: string | null; lastIp: string | null; revokedAt: string | null;
};
export type AuditEntry = { id: number; at: string; keyId: number; keyName: string; ip: string; action: string; status: string };

export type DeviceKind = { driver: string; label: string; configured: boolean; ready: boolean; error: string };
export type DevicesInfo = {
  drivers: Record<string, string[]>;
  switcher: DeviceKind; presentation: DeviceKind; mixer: DeviceKind;
  favorites: { slot: number; label: string; channel: number | null }[];
};
