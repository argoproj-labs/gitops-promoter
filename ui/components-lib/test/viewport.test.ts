import { describe, expect, it } from 'vitest';
import {
  DRAG_THRESHOLD,
  MAP_EXPAND_THRESHOLD,
  MAP_MAX_HEIGHT,
  MAP_MIN_HEIGHT,
  SPLIT_KEY_STEP,
  ZOOM_MAX,
  ZOOM_MIN,
  clampSplitHeight,
  clampZoom,
  exceedsDragThreshold,
  fitZoom,
  panOffset,
  preferredMapHeight,
  resolveSplitResize,
  splitKeyHeight,
  stepZoom,
  wheelZoom,
  zoomAboutPoint,
} from '@lib/components/PromotionExplorer/viewport';

describe('clampZoom', () => {
  it('clamps to the 50-150 range', () => {
    expect(clampZoom(10)).toBe(ZOOM_MIN);
    expect(clampZoom(500)).toBe(ZOOM_MAX);
    expect(clampZoom(87)).toBe(87);
  });
});

describe('stepZoom', () => {
  it('moves by 10 and never leaves the range', () => {
    expect(stepZoom(100, 1)).toBe(110);
    expect(stepZoom(100, -1)).toBe(90);
    expect(stepZoom(145, 1)).toBe(ZOOM_MAX);
    expect(stepZoom(55, -1)).toBe(ZOOM_MIN);
  });
});

describe('wheelZoom', () => {
  it('zooms in on wheel up and out on wheel down in steps of 5', () => {
    expect(wheelZoom(100, -120)).toBe(105);
    expect(wheelZoom(100, 120)).toBe(95);
  });

  it('clamps at the limits', () => {
    expect(wheelZoom(ZOOM_MAX, -1)).toBe(ZOOM_MAX);
    expect(wheelZoom(ZOOM_MIN, 1)).toBe(ZOOM_MIN);
  });
});

describe('zoomAboutPoint', () => {
  it('keeps the point under the cursor fixed in graph space', () => {
    const point = { x: 120, y: -40 };
    const offset = { x: 30, y: 10 };
    const result = zoomAboutPoint(100, offset, 120, point);
    const before = { x: (point.x - offset.x) / 1, y: (point.y - offset.y) / 1 };
    const after = {
      x: (point.x - result.offset.x) / (result.zoom / 100),
      y: (point.y - result.offset.y) / (result.zoom / 100),
    };
    expect(result.zoom).toBe(120);
    expect(after.x).toBeCloseTo(before.x);
    expect(after.y).toBeCloseTo(before.y);
  });

  it('is a no-op around the origin with zero offset', () => {
    expect(zoomAboutPoint(100, { x: 0, y: 0 }, 110, { x: 0, y: 0 })).toEqual({
      zoom: 110,
      offset: { x: 0, y: 0 },
    });
  });

  it('clamps the requested zoom', () => {
    expect(zoomAboutPoint(140, { x: 0, y: 0 }, 400, { x: 10, y: 10 }).zoom).toBe(ZOOM_MAX);
  });
});

describe('fitZoom', () => {
  it('fits the content inside the viewport minus chrome margins', () => {
    expect(fitZoom({ width: 1096, height: 600 }, { width: 1000, height: 200 })).toBe(100);
  });

  it('is limited by the tighter axis', () => {
    expect(fitZoom({ width: 2000, height: 232 }, { width: 400, height: 200 })).toBe(50);
  });

  it('never exceeds the zoom range', () => {
    expect(fitZoom({ width: 5000, height: 5000 }, { width: 100, height: 100 })).toBe(ZOOM_MAX);
    expect(fitZoom({ width: 10, height: 10 }, { width: 1000, height: 1000 })).toBe(ZOOM_MIN);
  });

  it('falls back to 100 for empty content', () => {
    expect(fitZoom({ width: 800, height: 400 }, { width: 0, height: 0 })).toBe(100);
  });
});

describe('drag helpers', () => {
  it('detects movement past the 4px threshold', () => {
    expect(exceedsDragThreshold(1, 1)).toBe(false);
    expect(exceedsDragThreshold(3, 0)).toBe(false);
    expect(exceedsDragThreshold(DRAG_THRESHOLD, 0)).toBe(true);
    expect(exceedsDragThreshold(3, 3)).toBe(true);
  });

  it('translates the origin offset by the drag delta', () => {
    expect(panOffset({ x: 10, y: -5 }, 7, 3)).toEqual({ x: 17, y: -2 });
  });
});

describe('preferredMapHeight', () => {
  it('sizes to the content plus chrome, within bounds', () => {
    expect(preferredMapHeight(260, 1000)).toBe(392);
  });

  it('keeps a floor for short graphs', () => {
    expect(preferredMapHeight(92, 1000)).toBe(MAP_MIN_HEIGHT);
  });

  it('is capped at 54% of the viewport and the absolute maximum', () => {
    expect(preferredMapHeight(2000, 600)).toBe(324);
    expect(preferredMapHeight(2000, 2000)).toBe(MAP_MAX_HEIGHT);
  });

  it('never exceeds a tiny viewport ceiling', () => {
    expect(preferredMapHeight(260, 300)).toBe(162);
  });
});

describe('split helpers', () => {
  it('clamps between the minimum and maximum', () => {
    expect(clampSplitHeight(10, 72, 500)).toBe(72);
    expect(clampSplitHeight(900, 72, 500)).toBe(500);
    expect(clampSplitHeight(300, 72, 500)).toBe(300);
  });

  it('never returns less than the minimum even when the max is smaller', () => {
    expect(clampSplitHeight(300, 72, 40)).toBe(72);
  });

  it('moves by 24px on arrow keys and ignores other keys', () => {
    expect(splitKeyHeight(200, 'ArrowDown', 72, 500)).toBe(200 + SPLIT_KEY_STEP);
    expect(splitKeyHeight(200, 'ArrowUp', 72, 500)).toBe(200 - SPLIT_KEY_STEP);
    expect(splitKeyHeight(200, 'Enter', 72, 500)).toBeNull();
    expect(splitKeyHeight(80, 'ArrowUp', 72, 500)).toBe(72);
    expect(splitKeyHeight(80, 'ArrowDown', 72, 500)).toBe(MAP_EXPAND_THRESHOLD + 1);
  });

  it('collapses on null or small heights and expands past the threshold', () => {
    expect(resolveSplitResize(null)).toEqual({ kind: 'collapse' });
    expect(resolveSplitResize(140)).toEqual({ kind: 'collapse' });
    expect(resolveSplitResize(141)).toEqual({ kind: 'expand', height: 141 });
  });
});
