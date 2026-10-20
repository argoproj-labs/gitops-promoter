import type { MouseEvent as ReactMouseEvent, PointerEvent as ReactPointerEvent } from 'react';
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { usePanZoom } from '@lib/components/PromotionExplorer/usePanZoom';

let canvas: HTMLDivElement;
let chrome: HTMLButtonElement;

function setSize(width: number, height: number) {
  Object.defineProperty(canvas, 'clientWidth', { configurable: true, value: width });
  Object.defineProperty(canvas, 'clientHeight', { configurable: true, value: height });
  canvas.getBoundingClientRect = () =>
    ({ left: 0, top: 0, width, height, right: width, bottom: height, x: 0, y: 0 }) as DOMRect;
}

beforeEach(() => {
  canvas = document.createElement('div');
  chrome = document.createElement('button');
  chrome.className = 'chrome';
  canvas.append(chrome);
  document.body.append(canvas);
  setSize(400, 200);
});

afterEach(() => {
  canvas.remove();
});

const content = { width: 1000, height: 200 };

function setup(wheelEnabled = true) {
  return renderHook(
    (props: { wheelEnabled: boolean }) =>
      usePanZoom({
        canvasRef: { current: canvas },
        content,
        wheelEnabled: props.wheelEnabled,
        ignoreSelector: '.chrome',
      }),
    { initialProps: { wheelEnabled } },
  );
}

function pointer(
  overrides: Partial<{
    button: number;
    clientX: number;
    clientY: number;
    pointerId: number;
    target: HTMLElement;
  }> = {},
) {
  const setPointerCapture = vi.fn();
  const event = {
    button: 0,
    clientX: 0,
    clientY: 0,
    pointerId: 1,
    target: canvas,
    currentTarget: { setPointerCapture },
    ...overrides,
  } as unknown as ReactPointerEvent<HTMLElement>;
  return { event, setPointerCapture };
}

describe('usePanZoom zoom controls', () => {
  it('starts at 100% with no offset', () => {
    const { result } = setup();
    expect(result.current.zoom).toBe(100);
    expect(result.current.offset).toEqual({ x: 0, y: 0 });
    expect(result.current.panning).toBe(false);
  });

  it('steps by 10 and clamps to 50-150', () => {
    const { result } = setup();
    act(() => result.current.zoomIn());
    expect(result.current.zoom).toBe(110);
    for (let i = 0; i < 10; i++) act(() => result.current.zoomIn());
    expect(result.current.zoom).toBe(150);
    act(() => result.current.zoomOut());
    expect(result.current.zoom).toBe(140);
    for (let i = 0; i < 15; i++) act(() => result.current.zoomOut());
    expect(result.current.zoom).toBe(50);
  });

  it('reset restores 100% and clears the pan', () => {
    const { result } = setup();
    act(() => result.current.zoomIn());
    act(() => {
      const { event } = pointer();
      result.current.canvasHandlers.onPointerDown(event);
      result.current.canvasHandlers.onPointerMove(pointer({ clientX: 40 }).event);
    });
    expect(result.current.offset.x).toBe(40);
    act(() => result.current.reset());
    expect(result.current.zoom).toBe(100);
    expect(result.current.offset).toEqual({ x: 0, y: 0 });
  });

  it('fit computes zoom from the canvas size and clears the pan', () => {
    setSize(1096, 600);
    const { result } = setup();
    act(() => result.current.zoomOut());
    act(() => result.current.fit());
    expect(result.current.zoom).toBe(100);
    expect(result.current.offset).toEqual({ x: 0, y: 0 });
  });

  it('fit clamps to the minimum for narrow canvases', () => {
    setSize(300, 600);
    const { result } = setup();
    act(() => result.current.fit());
    expect(result.current.zoom).toBe(50);
  });
});

describe('usePanZoom wheel', () => {
  it('zooms around the cursor and prevents page scroll', () => {
    const { result } = setup();
    const event = new WheelEvent('wheel', {
      deltaY: -100,
      clientX: 300,
      clientY: 100,
      cancelable: true,
      bubbles: true,
    });
    act(() => {
      canvas.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(true);
    expect(result.current.zoom).toBe(105);
    expect(result.current.offset.x).toBeCloseTo(100 - 100 * 1.05);
    expect(result.current.offset.y).toBeCloseTo(0);
  });

  it('zooms out on positive deltaY and clamps at the limits', () => {
    const { result } = setup();
    for (let i = 0; i < 20; i++) {
      act(() => {
        canvas.dispatchEvent(new WheelEvent('wheel', { deltaY: 100, cancelable: true }));
      });
    }
    expect(result.current.zoom).toBe(50);
    for (let i = 0; i < 30; i++) {
      act(() => {
        canvas.dispatchEvent(new WheelEvent('wheel', { deltaY: -100, cancelable: true }));
      });
    }
    expect(result.current.zoom).toBe(150);
  });

  it('does nothing while disabled and does not prevent scrolling', () => {
    const { result, rerender } = setup(false);
    const event = new WheelEvent('wheel', { deltaY: -100, cancelable: true });
    act(() => {
      canvas.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(false);
    expect(result.current.zoom).toBe(100);
    rerender({ wheelEnabled: true });
    act(() => {
      canvas.dispatchEvent(new WheelEvent('wheel', { deltaY: -100, cancelable: true }));
    });
    expect(result.current.zoom).toBe(105);
  });

  it('registers a non-passive native listener and removes it on unmount', () => {
    const add = vi.spyOn(canvas, 'addEventListener');
    const remove = vi.spyOn(canvas, 'removeEventListener');
    const { unmount } = setup();
    expect(add).toHaveBeenCalledWith('wheel', expect.any(Function), { passive: false });
    unmount();
    expect(remove).toHaveBeenCalledWith('wheel', expect.any(Function));
  });
});

describe('usePanZoom dragging', () => {
  it('ignores movement below the 4px threshold', () => {
    const { result } = setup();
    act(() => result.current.canvasHandlers.onPointerDown(pointer().event));
    act(() =>
      result.current.canvasHandlers.onPointerMove(pointer({ clientX: 2, clientY: 2 }).event),
    );
    expect(result.current.panning).toBe(false);
    expect(result.current.offset).toEqual({ x: 0, y: 0 });
  });

  it('pans once the threshold is crossed and captures the pointer', () => {
    const { result } = setup();
    act(() => result.current.canvasHandlers.onPointerDown(pointer().event));
    const move = pointer({ clientX: 30, clientY: -12 });
    act(() => result.current.canvasHandlers.onPointerMove(move.event));
    expect(result.current.panning).toBe(true);
    expect(result.current.offset).toEqual({ x: 30, y: -12 });
    expect(move.setPointerCapture).toHaveBeenCalledWith(1);
    act(() =>
      result.current.canvasHandlers.onPointerMove(pointer({ clientX: 50, clientY: 0 }).event),
    );
    expect(result.current.offset).toEqual({ x: 50, y: 0 });
  });

  it('continues from the previous offset on the next drag', () => {
    const { result } = setup();
    act(() => result.current.canvasHandlers.onPointerDown(pointer().event));
    act(() => result.current.canvasHandlers.onPointerMove(pointer({ clientX: 20 }).event));
    act(() => result.current.canvasHandlers.onPointerUp(pointer({ clientX: 20 }).event));
    act(() => result.current.canvasHandlers.onPointerDown(pointer({ clientX: 100 }).event));
    act(() => result.current.canvasHandlers.onPointerMove(pointer({ clientX: 110 }).event));
    expect(result.current.offset.x).toBe(30);
  });

  it('suppresses exactly one click after a drag', () => {
    const { result } = setup();
    act(() => result.current.canvasHandlers.onPointerDown(pointer().event));
    act(() => result.current.canvasHandlers.onPointerMove(pointer({ clientX: 20 }).event));
    act(() => result.current.canvasHandlers.onPointerUp(pointer({ clientX: 20 }).event));
    expect(result.current.panning).toBe(false);

    const first = { stopPropagation: vi.fn(), preventDefault: vi.fn() };
    result.current.canvasHandlers.onClickCapture(first as unknown as ReactMouseEvent<HTMLElement>);
    expect(first.stopPropagation).toHaveBeenCalled();
    expect(first.preventDefault).toHaveBeenCalled();

    const second = { stopPropagation: vi.fn(), preventDefault: vi.fn() };
    result.current.canvasHandlers.onClickCapture(second as unknown as ReactMouseEvent<HTMLElement>);
    expect(second.stopPropagation).not.toHaveBeenCalled();
  });

  it('does not suppress clicks after a press without a drag', () => {
    const { result } = setup();
    act(() => result.current.canvasHandlers.onPointerDown(pointer().event));
    act(() => result.current.canvasHandlers.onPointerMove(pointer({ clientX: 1 }).event));
    act(() => result.current.canvasHandlers.onPointerUp(pointer({ clientX: 1 }).event));
    const click = { stopPropagation: vi.fn(), preventDefault: vi.fn() };
    result.current.canvasHandlers.onClickCapture(click as unknown as ReactMouseEvent<HTMLElement>);
    expect(click.stopPropagation).not.toHaveBeenCalled();
  });

  it('ignores non-primary buttons, chrome targets and foreign pointers', () => {
    const { result } = setup();
    act(() => result.current.canvasHandlers.onPointerDown(pointer({ button: 2 }).event));
    act(() => result.current.canvasHandlers.onPointerMove(pointer({ clientX: 40 }).event));
    expect(result.current.offset.x).toBe(0);

    act(() => result.current.canvasHandlers.onPointerDown(pointer({ target: chrome }).event));
    act(() => result.current.canvasHandlers.onPointerMove(pointer({ clientX: 40 }).event));
    expect(result.current.offset.x).toBe(0);

    act(() => result.current.canvasHandlers.onPointerDown(pointer({ pointerId: 1 }).event));
    act(() =>
      result.current.canvasHandlers.onPointerMove(pointer({ pointerId: 2, clientX: 40 }).event),
    );
    expect(result.current.offset.x).toBe(0);
  });

  it('stops panning on pointer cancel', () => {
    const { result } = setup();
    act(() => result.current.canvasHandlers.onPointerDown(pointer().event));
    act(() => result.current.canvasHandlers.onPointerMove(pointer({ clientX: 20 }).event));
    expect(result.current.panning).toBe(true);
    act(() => result.current.canvasHandlers.onPointerCancel(pointer().event));
    expect(result.current.panning).toBe(false);
  });
});
