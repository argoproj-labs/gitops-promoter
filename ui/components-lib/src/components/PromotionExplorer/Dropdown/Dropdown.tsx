import React, { useEffect, useState, useCallback, useRef } from 'react';
import { createPortal } from 'react-dom';
import { FaChevronDown, FaCheck } from 'react-icons/fa';

export const Dropdown: React.FC<{
  icon: React.ReactNode;
  label: string;
  value: React.ReactNode;
  active?: boolean;
  children: (close: () => void) => React.ReactNode;
}> = ({ icon, label, value, active, children }) => {
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const [coords, setCoords] = useState<{ x: number; y: number } | null>(null);
  const open = coords !== null;

  const close = useCallback(() => setCoords(null), []);

  const toggle = useCallback(() => {
    setCoords((prev) => {
      if (prev) return null;
      const el = triggerRef.current;
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { x: r.left, y: r.bottom + 6 };
    });
  }, []);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const el = triggerRef.current;
      const menu = document.getElementById('hp-dropdown-menu');
      const t = e.target as Node;
      if (el?.contains(t) || menu?.contains(t)) return;
      close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close();
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open, close]);

  return (
    <div className="hp-dd">
      <button
        ref={triggerRef}
        type="button"
        className={`hp-dd__trigger ${active ? 'hp-dd__trigger--active' : ''} ${open ? 'hp-dd__trigger--open' : ''}`}
        onClick={toggle}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={label}
        title={label}
      >
        <span className="hp-dd__icon" aria-hidden="true">
          {icon}
        </span>
        <span className="hp-dd__value">{value}</span>
        <FaChevronDown className="hp-dd__caret" aria-hidden="true" />
      </button>
      {open &&
        coords &&
        createPortal(
          <div
            id="hp-dropdown-menu"
            role="listbox"
            className="hp-dd__menu"
            style={{ left: coords.x, top: coords.y }}
          >
            {children(close)}
          </div>,
          document.body,
        )}
    </div>
  );
};

export const DropdownItem: React.FC<{
  selected: boolean;
  onSelect: () => void;
  multi?: boolean;
  children: React.ReactNode;
}> = ({ selected, onSelect, multi, children }) => (
  <button
    type="button"
    role="option"
    aria-selected={selected}
    className={`hp-dd__item ${selected ? 'hp-dd__item--selected' : ''}`}
    onClick={onSelect}
  >
    {multi && (
      <span className="hp-dd__check" aria-hidden="true">
        {selected && <FaCheck />}
      </span>
    )}
    {children}
  </button>
);
