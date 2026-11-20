import { createContext, useContext } from "react";
import { useShared } from "./hooks";

// Which camera a widget drives. There is no screen-wide "current camera":
// each camera widget has its own Camera setting and provides it here, and
// everything inside it (moves, presets, picture) talks to that camera.
// "" means the main camera.

export const CameraContext = createContext("");

export function useCam(): string {
  return useContext(CameraContext);
}

/** Adds ?cam= to an API path ("" = the main camera: nothing added). */
export function withCam(path: string, cam: string): string {
  return cam ? `${path}${path.includes("?") ? "&" : "?"}cam=${encodeURIComponent(cam)}` : path;
}

/** The name of this widget's camera, for its title — only when there is
 *  more than one camera to tell apart. */
export function useCamTitle(): string {
  const cam = useCam();
  const { data } = useShared<{ id: string; name: string; main?: boolean }[]>("/api/cameras", 10000);
  if (!data || data.length < 2) return "";
  return (data.find((c) => c.id === cam) ?? data.find((c) => c.main))?.name ?? "";
}
