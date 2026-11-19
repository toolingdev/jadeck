import { useCallback, useEffect, useRef, useState } from "react";

// Drag a widget from the library with a finger (HTML5 drag-and-drop only
// works with a mouse). Press and hold ~200 ms to pick it up, so a quick
// swipe still scrolls the library. While dragging, page scrolling is
// suppressed and the page auto-scrolls near the top/bottom edge.

export type TouchDrag<T> = { item: T; x: number; y: number } | null;

export function useTouchDrag<T>(onDrop: (item: T, x: number, y: number) => void) {
  const [drag, setDrag] = useState<TouchDrag<T>>(null);
  const st = useRef<{ item: T; sx: number; sy: number; x: number; y: number; armed: boolean; timer?: number; id: number } | null>(null);
  const dropRef = useRef(onDrop);
  dropRef.current = onDrop;
  const justDragged = useRef(false);

  const cleanup = useCallback(() => {
    const s = st.current;
    if (s?.timer) clearTimeout(s.timer);
    st.current = null;
    setDrag(null);
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("pointerup", onUp);
    window.removeEventListener("pointercancel", onCancel);
    window.removeEventListener("touchmove", onTouchMove);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function onMove(e: PointerEvent) {
    const s = st.current;
    if (!s || e.pointerId !== s.id) return;
    s.x = e.clientX;
    s.y = e.clientY;
    if (!s.armed) {
      if (Math.hypot(e.clientX - s.sx, e.clientY - s.sy) > 10) cleanup(); // it's a scroll
      return;
    }
    setDrag({ item: s.item, x: s.x, y: s.y });
  }
  function onTouchMove(e: TouchEvent) {
    if (st.current?.armed) e.preventDefault(); // hold the page still while dragging
  }
  function onUp(e: PointerEvent) {
    const s = st.current;
    if (!s || e.pointerId !== s.id) return;
    if (s.armed) {
      justDragged.current = true;
      setTimeout(() => (justDragged.current = false), 400);
      dropRef.current(s.item, e.clientX, e.clientY);
    }
    cleanup();
  }
  function onCancel() {
    cleanup();
  }

  const start = useCallback((item: T, e: React.PointerEvent) => {
    if (e.pointerType === "mouse") return; // mouse uses native drag-and-drop
    cleanup();
    st.current = { item, sx: e.clientX, sy: e.clientY, x: e.clientX, y: e.clientY, armed: false, id: e.pointerId };
    st.current.timer = window.setTimeout(() => {
      const s = st.current;
      if (!s) return;
      s.armed = true;
      navigator.vibrate?.(10);
      setDrag({ item: s.item, x: s.x, y: s.y });
    }, 200);
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onCancel);
    window.addEventListener("touchmove", onTouchMove, { passive: false });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cleanup]);

  // Auto-scroll near the edges while dragging.
  useEffect(() => {
    if (!drag) return;
    const t = window.setInterval(() => {
      const s = st.current;
      if (!s?.armed) return;
      const edge = 90;
      if (s.y < edge) window.scrollBy(0, -Math.ceil((edge - s.y) / 6));
      else if (s.y > innerHeight - edge) window.scrollBy(0, Math.ceil((s.y - (innerHeight - edge)) / 6));
    }, 16);
    return () => clearInterval(t);
  }, [drag !== null]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => cleanup, [cleanup]);

  /** True right after a drag ends: swallow the click that follows. */
  const swallowClick = () => justDragged.current;
  return { drag, start, swallowClick };
}
