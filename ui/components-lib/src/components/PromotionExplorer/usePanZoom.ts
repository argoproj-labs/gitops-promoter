import { useCallback, useEffect, useRef, useState } from 'react';
import type { PointerEvent as ReactPointerEvent, RefObject } from 'react';
import {
  ZOOM_DEFAULT,
  exceedsDragThreshold,
  fitZoom,
  panOffset,
  stepZoom,
  wheelZoom,
  zoomAboutPoint,
  type Offset,
  type Size,
} from './viewport';

export interface UsePanZoomOptions {
  canvasRef: RefObject<HTMLElement | null>;
  content: Size;
  wheelEnabled: boolean;
  ignoreSelector?: string;
}

interface DragState {
  x: number;
  y: number;
  origin: Offset;
  pointerId: number;
  moved: boolean;
}

const ORIGIN: Offset = { x: 0, y: 0 };

export function usePanZoom({
  canvasRef,
  content,
  wheelEnabled,
  ignoreSelector,
}: UsePanZoomOptions) {
  const [zoom, setZoom] = useState(ZOOM_DEFAULT);
  const [offset, setOffset] = useState<Offset>(ORIGIN);
  const [panning, setPanning] = useState(false);

  const zoomRef = useRef(zoom);
  const offsetRef = useRef(offset);
  const wheelEnabledRef = useRef(wheelEnabled);
  const drag = useRef<DragState | null>(null);
  const suppressClick = useRef(false);
  const manualZoom = useRef(false);
  zoomRef.current = zoom;
  offsetRef.current = offset;
  wheelEnabledRef.current = wheelEnabled;

  const fit = useCallback(
    (height?: number) => {
      const el = canvasRef.current;
      if (!el) return;
      manualZoom.current = false;
      setZoom(fitZoom({ width: el.clientWidth, height: height ?? el.clientHeight }, content));
      setOffset(ORIGIN);
    },
    [canvasRef, content],
  );

  const reset = useCallback(() => {
    manualZoom.current = false;
    setZoom(ZOOM_DEFAULT);
    setOffset(ORIGIN);
  }, []);

  const zoomIn = useCallback(() => {
    manualZoom.current = true;
    setZoom((z) => stepZoom(z, 1));
  }, []);
  const zoomOut = useCallback(() => {
    manualZoom.current = true;
    setZoom((z) => stepZoom(z, -1));
  }, []);
  const isManualZoom = useCallback(() => manualZoom.current, []);

  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    const onWheel = (event: WheelEvent) => {
      if (!wheelEnabledRef.current) return;
      event.preventDefault();
      manualZoom.current = true;
      const rect = el.getBoundingClientRect();
      const point = {
        x: event.clientX - rect.left - rect.width / 2,
        y: event.clientY - rect.top - rect.height / 2,
      };
      const next = zoomAboutPoint(
        zoomRef.current,
        offsetRef.current,
        wheelZoom(zoomRef.current, event.deltaY),
        point,
      );
      setZoom(next.zoom);
      setOffset(next.offset);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [canvasRef]);

  const onPointerDown = useCallback(
    (event: ReactPointerEvent<HTMLElement>) => {
      if (event.button !== 0) return;
      if (ignoreSelector && (event.target as HTMLElement).closest(ignoreSelector)) return;
      suppressClick.current = false;
      drag.current = {
        x: event.clientX,
        y: event.clientY,
        origin: offsetRef.current,
        pointerId: event.pointerId,
        moved: false,
      };
    },
    [ignoreSelector],
  );

  const onPointerMove = useCallback((event: ReactPointerEvent<HTMLElement>) => {
    const d = drag.current;
    if (!d || d.pointerId !== event.pointerId) return;
    const dx = event.clientX - d.x;
    const dy = event.clientY - d.y;
    if (!d.moved) {
      if (!exceedsDragThreshold(dx, dy)) return;
      d.moved = true;
      setPanning(true);
      event.currentTarget.setPointerCapture?.(event.pointerId);
    }
    setOffset(panOffset(d.origin, dx, dy));
  }, []);

  const onPointerEnd = useCallback((event: ReactPointerEvent<HTMLElement>) => {
    const d = drag.current;
    if (!d || d.pointerId !== event.pointerId) return;
    if (d.moved) suppressClick.current = true;
    drag.current = null;
    setPanning(false);
  }, []);

  const onClickCapture = useCallback((event: React.MouseEvent<HTMLElement>) => {
    if (!suppressClick.current) return;
    event.stopPropagation();
    event.preventDefault();
    suppressClick.current = false;
  }, []);

  return {
    zoom,
    offset,
    panning,
    fit,
    reset,
    isManualZoom,
    zoomIn,
    zoomOut,
    canvasHandlers: {
      onPointerDown,
      onPointerMove,
      onPointerUp: onPointerEnd,
      onPointerCancel: onPointerEnd,
      onClickCapture,
    },
  };
}
