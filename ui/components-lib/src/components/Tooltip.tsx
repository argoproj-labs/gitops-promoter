import React, { useState, useRef, useEffect } from 'react';
import './Tooltip.scss';

export const useHover = (): [React.MutableRefObject<HTMLDivElement | null>, boolean] => {
  const [show, setShow] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);

  const handleMouseOver = () => setShow(true);
  const handleMouseOut = () => setShow(false);
  const handleFocus = () => setShow(true);
  const handleBlur = () => setShow(false);

  useEffect(() => {
    const cur = ref.current;

    if (cur) {
      cur.addEventListener('mouseover', handleMouseOver);
      cur.addEventListener('mouseout', handleMouseOut);
      cur.addEventListener('focus', handleFocus, true);
      cur.addEventListener('blur', handleBlur, true);

      return () => {
        cur.removeEventListener('mouseover', handleMouseOver);
        cur.removeEventListener('mouseout', handleMouseOut);
        cur.removeEventListener('focus', handleFocus, true);
        cur.removeEventListener('blur', handleBlur, true);
      };
    }
  }, []);

  return [ref, show];
};

interface TooltipProps {
  content: React.ReactNode | string;
  children: React.ReactNode;
}

/**
 * Displays a Tooltip when its children are hovered over
 */
export const Tooltip: React.FC<TooltipProps> = ({ content, children }) => {
  const [tooltip, showTooltip] = useHover();
  const [position, setPosition] = useState({ top: 0, left: 0 });

  useEffect(() => {
    if (showTooltip && tooltip.current) {
      const rect = tooltip.current.getBoundingClientRect();
      setPosition({
        top: rect.bottom + 4,
        left: rect.left,
      });
    }
  }, [showTooltip]);

  return (
    <div ref={tooltip} className="promoter-popover-wrapper">
      {showTooltip && content && (
        <div
          className="promoter-popover"
          style={{
            top: `${position.top}px`,
            left: `${position.left}px`,
          }}
        >
          <div className="promoter-popover-content">{content}</div>
        </div>
      )}
      {children}
    </div>
  );
};
