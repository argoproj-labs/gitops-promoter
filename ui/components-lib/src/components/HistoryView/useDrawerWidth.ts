import { useEffect, useState, useCallback } from 'react';
import {
  DRAWER_MIN_WIDTH,
  DRAWER_MAX_WIDTH,
  DRAWER_DEFAULT_WIDTH,
  DRAWER_WIDTH_KEY,
} from './presentation';

export function useDrawerWidth() {
  const [width, setWidth] = useState<number>(() => {
    const saved = Number(localStorage.getItem(DRAWER_WIDTH_KEY));
    return saved >= DRAWER_MIN_WIDTH && saved <= DRAWER_MAX_WIDTH ? saved : DRAWER_DEFAULT_WIDTH;
  });
  const [isResizing, setIsResizing] = useState(false);

  useEffect(() => {
    localStorage.setItem(DRAWER_WIDTH_KEY, String(width));
  }, [width]);

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
      document.body.style.userSelect = 'none';
      document.body.style.cursor = 'col-resize';
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
    },
    [width],
  );

  const onResizeReset = useCallback(() => {
    setWidth(DRAWER_DEFAULT_WIDTH);
  }, []);

  const onResizeTo = useCallback((next: number) => {
    setWidth(Math.min(DRAWER_MAX_WIDTH, Math.max(DRAWER_MIN_WIDTH, next)));
  }, []);

  return { width, isResizing, onResizeStart, onResizeReset, onResizeTo };
}
