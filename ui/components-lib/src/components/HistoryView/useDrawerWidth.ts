import { useEffect, useState, useCallback } from 'react';
import {
  DRAWER_MIN_WIDTH,
  DRAWER_MAX_WIDTH,
  DRAWER_DEFAULT_WIDTH,
  DRAWER_WIDTH_KEY,
} from './presentation';

/**
 * User-resizable detail drawer state. Width persists across sessions
 * (localStorage) and is clamped so the drawer can't swallow or vanish from the
 * viewport. Exposes the pointer-drag, double-click-reset, and keyboard-resize
 * handlers the drawer's resizer needs.
 */
export function useDrawerWidth() {
  const [width, setWidth] = useState<number>(() => {
    const saved = Number(localStorage.getItem(DRAWER_WIDTH_KEY));
    return saved >= DRAWER_MIN_WIDTH && saved <= DRAWER_MAX_WIDTH ? saved : DRAWER_DEFAULT_WIDTH;
  });
  const [isResizing, setIsResizing] = useState(false);

  useEffect(() => {
    localStorage.setItem(DRAWER_WIDTH_KEY, String(width));
  }, [width]);

  /** Begin dragging the drawer's left edge. We track from the pointer's start
   *  X and the width at grab time, then resize live until pointerup. */
  const onResizeStart = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      const startX = e.clientX;
      const startWidth = width;
      setIsResizing(true);

      const onMove = (ev: PointerEvent) => {
        // Drawer is anchored right, so dragging left (smaller clientX) widens it.
        const delta = startX - ev.clientX;
        const next = Math.min(DRAWER_MAX_WIDTH, Math.max(DRAWER_MIN_WIDTH, startWidth + delta));
        setWidth(next);
      };
      const onUp = () => {
        setIsResizing(false);
        window.removeEventListener('pointermove', onMove);
        window.removeEventListener('pointerup', onUp);
        document.body.style.userSelect = '';
        document.body.style.cursor = '';
      };
      // Suppress text selection / cursor flicker while dragging.
      document.body.style.userSelect = 'none';
      document.body.style.cursor = 'col-resize';
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
    },
    [width],
  );

  /** Reset the drawer to its default width (double-click the resize handle). */
  const onResizeReset = useCallback(() => {
    setWidth(DRAWER_DEFAULT_WIDTH);
  }, []);

  /** Set an absolute, clamped drawer width (used by keyboard resize). */
  const onResizeTo = useCallback((next: number) => {
    setWidth(Math.min(DRAWER_MAX_WIDTH, Math.max(DRAWER_MIN_WIDTH, next)));
  }, []);

  return { width, isResizing, onResizeStart, onResizeReset, onResizeTo };
}
