import { createContext, useContext } from "react";

// Which camera a widget drives. There is no screen-wide "current camera":
// each camera widget has its own Camera setting and provides it here, and
// everything inside it (moves, presets, picture) talks to that camera.
// "" means the main camera.

export const CameraContext = createContext("");

/** Switches the camera from the widget itself (null where it can't). */
export const CameraPickContext = createContext<((id: string) => void) | null>(null);

export function useCamPick() {
  return useContext(CameraPickContext);
}

export function useCam(): string {
  return useContext(CameraContext);
}

/** Adds ?cam= to an API path ("" = the main camera: nothing added). */
export function withCam(path: string, cam: string): string {
  return cam ? `${path}${path.includes("?") ? "&" : "?"}cam=${encodeURIComponent(cam)}` : path;
}
