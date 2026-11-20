import { createContext, useContext } from "react";
import { screenFor } from "./types";

/** How many video widgets the dashboard on screen holds (for Auto quality). */
export const VideoCount = createContext(1);

export const VIDEO_TYPES = new Set(["camera.live", "switcher.program", "switcher.confidence"]);

/**
 * Auto quality: HD when this is the only video on a tablet/desktop
 * screen; SD (the camera's second, lighter stream) when several share
 * the screen or on a phone — less Wi-Fi, cooler devices, no visible loss
 * at that size.
 */
export function useAutoSD(quality: string) {
  const n = useContext(VideoCount);
  if (quality === "sd") return true;
  if (quality === "hd") return false;
  return n > 1 || screenFor(window.innerWidth) === "phone";
}
