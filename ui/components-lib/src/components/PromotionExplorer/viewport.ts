export const ZOOM_MIN = 50;
export const ZOOM_MAX = 150;
export const ZOOM_DEFAULT = 100;
export const ZOOM_STEP = 10;
export const WHEEL_ZOOM_STEP = 5;
export const DRAG_THRESHOLD = 4;
export const FIT_MARGIN_X = 96;
export const FIT_MARGIN_Y = 132;

export const MAP_COLLAPSED_HEIGHT = 80;
export const MAP_MIN_HEIGHT = 240;
export const MAP_MAX_HEIGHT = 524;
export const MAP_EXPAND_THRESHOLD = 140;
export const SPLIT_MIN_HEIGHT = 72;
export const SPLIT_KEY_STEP = 24;
export const SPLIT_RESERVED_HEIGHT = 160;

export interface Offset {
  x: number;
  y: number;
}

export interface Size {
  width: number;
  height: number;
}

export function clampZoom(value: number, min = ZOOM_MIN, max = ZOOM_MAX): number {
  return Math.min(max, Math.max(min, value));
}

export function stepZoom(current: number, direction: 1 | -1, step = ZOOM_STEP): number {
  return clampZoom(current + direction * step);
}

export function wheelZoom(current: number, deltaY: number): number {
  return clampZoom(current + (deltaY < 0 ? WHEEL_ZOOM_STEP : -WHEEL_ZOOM_STEP));
}

export function zoomAboutPoint(
  zoom: number,
  offset: Offset,
  nextZoom: number,
  point: Offset,
): { zoom: number; offset: Offset } {
  const next = clampZoom(nextZoom);
  const ratio = next / zoom;
  return {
    zoom: next,
    offset: {
      x: point.x - (point.x - offset.x) * ratio,
      y: point.y - (point.y - offset.y) * ratio,
    },
  };
}

export function fitZoom(viewport: Size, content: Size): number {
  if (content.width <= 0 || content.height <= 0) return ZOOM_DEFAULT;
  const scale = Math.min(
    (viewport.width - FIT_MARGIN_X) / content.width,
    (viewport.height - FIT_MARGIN_Y) / content.height,
  );
  return Math.round(clampZoom(scale * 100));
}

export function exceedsDragThreshold(dx: number, dy: number, threshold = DRAG_THRESHOLD): boolean {
  return Math.hypot(dx, dy) >= threshold;
}

export function panOffset(origin: Offset, dx: number, dy: number): Offset {
  return { x: origin.x + dx, y: origin.y + dy };
}

export function preferredMapHeight(contentHeight: number, viewportHeight: number): number {
  const ceiling = Math.min(MAP_MAX_HEIGHT, Math.round(viewportHeight * 0.54));
  return Math.max(
    Math.min(MAP_MIN_HEIGHT, ceiling),
    Math.min(ceiling, Math.round(contentHeight + FIT_MARGIN_Y)),
  );
}

export function clampSplitHeight(height: number, min: number, max: number): number {
  return Math.min(Math.max(min, max), Math.max(min, height));
}

export function splitKeyHeight(
  current: number,
  key: string,
  min: number,
  max: number,
): number | null {
  if (key !== 'ArrowUp' && key !== 'ArrowDown') return null;
  if (key === 'ArrowUp') return clampSplitHeight(current - SPLIT_KEY_STEP, min, max);
  const next = Math.max(current + SPLIT_KEY_STEP, MAP_EXPAND_THRESHOLD + 1);
  return clampSplitHeight(next, min, max);
}

export type SplitResolution = { kind: 'collapse' } | { kind: 'expand'; height: number };

export function resolveSplitResize(
  height: number | null,
  threshold = MAP_EXPAND_THRESHOLD,
): SplitResolution {
  if (height == null || height <= threshold) return { kind: 'collapse' };
  return { kind: 'expand', height };
}
