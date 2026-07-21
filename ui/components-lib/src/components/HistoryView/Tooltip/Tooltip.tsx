import React, { useState, useCallback, useRef } from 'react';
import { createPortal } from 'react-dom';
import './_Tooltip.scss';

// Renders the bubble into document.body so it escapes the matrix cells'
// `overflow: hidden` (a CSS-only tooltip would be clipped).
const Tooltip: React.FC<{
  label: React.ReactNode;
  children: React.ReactElement;
}> = ({ label, children }) => {
  const triggerRef = useRef<HTMLElement | null>(null);
  const [coords, setCoords] = useState<{ x: number; y: number; below: boolean } | null>(null);

  const show = useCallback(() => {
    const el = triggerRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const below = r.top < 64;
    setCoords({
      x: r.left + r.width / 2,
      y: below ? r.bottom + 8 : r.top - 8,
      below,
    });
  }, []);

  const hide = useCallback(() => setCoords(null), []);

  // No wrapper element, so the trigger keeps its exact place in the surrounding flex layout.
  const child = children as React.ReactElement<Record<string, unknown>>;
  const childProps = child.props;
  const trigger = React.cloneElement(child, {
    ref: (node: HTMLElement | null) => {
      triggerRef.current = node;
      const r = (child as unknown as { ref?: unknown }).ref;
      if (typeof r === 'function') (r as (n: HTMLElement | null) => void)(node);
      else if (r && typeof r === 'object') (r as { current: unknown }).current = node;
    },
    onMouseEnter: (e: React.MouseEvent) => {
      show();
      (childProps.onMouseEnter as ((e: React.MouseEvent) => void) | undefined)?.(e);
    },
    onMouseLeave: (e: React.MouseEvent) => {
      hide();
      (childProps.onMouseLeave as ((e: React.MouseEvent) => void) | undefined)?.(e);
    },
    onFocus: (e: React.FocusEvent) => {
      show();
      (childProps.onFocus as ((e: React.FocusEvent) => void) | undefined)?.(e);
    },
    onBlur: (e: React.FocusEvent) => {
      hide();
      (childProps.onBlur as ((e: React.FocusEvent) => void) | undefined)?.(e);
    },
  } as Record<string, unknown>);

  return (
    <>
      {trigger}
      {coords &&
        createPortal(
          <span
            role="tooltip"
            className={`hp-tip ${coords.below ? 'hp-tip--below' : 'hp-tip--above'}`}
            style={{ left: coords.x, top: coords.y }}
          >
            {label}
          </span>,
          document.body,
        )}
    </>
  );
};

export default Tooltip;
