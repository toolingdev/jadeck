/** Capture the pointer so a hold survives the finger sliding off the
 * control. Capture can throw (pointer already gone, synthetic events);
 * a press must never fail because of it. */
export function capture(e: React.PointerEvent) {
  try {
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  } catch {
    /* hold still works without capture */
  }
}
