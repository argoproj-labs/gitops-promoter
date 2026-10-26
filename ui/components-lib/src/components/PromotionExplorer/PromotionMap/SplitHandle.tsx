import React, { useRef } from 'react';
import {
  SPLIT_MIN_HEIGHT,
  SPLIT_RESERVED_HEIGHT,
  clampSplitHeight,
  splitKeyHeight,
} from '../viewport';

const SplitHandle: React.FC<{
  target: React.RefObject<HTMLElement | null>;
  min?: number;
  onResize: (_height: number | null) => void;
  onResizeEnd?: () => void;
}> = ({ target, min = SPLIT_MIN_HEIGHT, onResize, onResizeEnd }) => {
  const start = useRef<{ y: number; height: number } | null>(null);

  const maxHeight = () => {
    const container = target.current?.parentElement?.clientHeight || window.innerHeight;
    return container - SPLIT_RESERVED_HEIGHT;
  };
  const finish = () => {
    if (!start.current) return;
    start.current = null;
    onResizeEnd?.();
  };
  const currentHeight = () => target.current?.offsetHeight ?? min;

  return (
    <div
      className="hp-split"
      role="separator"
      aria-orientation="horizontal"
      aria-label="Resize promotion map"
      tabIndex={0}
      onPointerDown={(event) => {
        start.current = { y: event.clientY, height: currentHeight() };
        event.currentTarget.setPointerCapture?.(event.pointerId);
      }}
      onPointerMove={(event) => {
        if (!start.current) return;
        onResize(
          clampSplitHeight(
            start.current.height + event.clientY - start.current.y,
            min,
            maxHeight(),
          ),
        );
      }}
      onPointerUp={finish}
      onPointerCancel={finish}
      onDoubleClick={() => onResize(null)}
      onKeyDown={(event) => {
        const next = splitKeyHeight(currentHeight(), event.key, min, maxHeight());
        if (next === null) return;
        event.preventDefault();
        onResize(next);
      }}
    >
      <span className="hp-split__grip" />
    </div>
  );
};

export default SplitHandle;
