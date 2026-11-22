// A camera's operator controls (PTZOptics): speeds, Motion Sync, focus
// lock, picture scenes, a preset's own recall speed. Kept with the camera
// on the server, so every screen shows the same values.
import { api } from "@/lib/api";
import { refreshShared, useShared } from "@/lib/hooks";
import { useCam, withCam } from "@/lib/camera";

export type Scene = { id: string; name: string; settings: Record<string, unknown> };
export type Controls = {
  camera: string;
  panSpeed: number; tiltSpeed: number; zoomSpeed: number; focusSpeed: number; recallSpeed: number;
  motionSync?: boolean; focusLocked: boolean;
  scenes: Scene[]; activeScene?: string; builtinScenes: Scene[];
  presetSpeeds?: Record<string, number>;
};

export const DEFAULT_CONTROLS: Controls = {
  camera: "", panSpeed: 24, tiltSpeed: 20, zoomSpeed: 7, focusSpeed: 0, recallSpeed: 0,
  focusLocked: false, scenes: [], builtinScenes: [],
};

/** The widget's camera's controls, plus a call that changes them and
 *  refreshes every widget showing them. */
export function useControls() {
  const cam = useCam();
  const path = withCam("/api/camera/controls", cam);
  const { data, reload } = useShared<Controls>(path, 8000);
  const call = async (method: string, p: string, body?: unknown) => {
    const r = await api<Controls>(withCam(p, cam), method, body);
    await refreshShared(path);
    return r;
  };
  return { data, ctl: data ?? DEFAULT_CONTROLS, reload, call, cam };
}
