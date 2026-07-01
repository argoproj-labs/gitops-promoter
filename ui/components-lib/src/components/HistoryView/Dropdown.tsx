import React, { useEffect, useState, useCallback, useRef } from 'react';
import { createPortal } from 'react-dom';
import { FaChevronDown, FaCheck } from 'react-icons/fa';

/** A control-bar dropdown: a chip-styled trigger showing the group's label and
 *  current selection, opening a portal menu positioned under the trigger so it
 *  escapes the controls row's overflow/stacking (same approach as Tooltip). The
 *  menu closes on outside-click, Escape, or when a child calls `close()`. */
export const Dropdown: React.FC<{
  /** Icon shown at the start of the trigger, standing in for the group name. */
  icon: React.ReactNode;
  /** Accessible name for the group (e.g. "Filter") — used for the trigger's
   *  aria-label/title since the icon carries no text. */
  label: string;
  /** The current selection, rendered inside the trigger (text or a colored
   *  swatch + name for the env dropdown). */
  value: React.ReactNode;
  /** True when a non-default option is selected — gives the trigger the active
   *  accent treatment so an applied filter reads at a glance. */
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
        <span className="hp-dd__icon" aria-hidden="true">{icon}</span>
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

/** One selectable row inside a Dropdown menu. Pass `multi` for options in a
 *  multi-select group — it renders a leading checkbox so it's clear more than
 *  one can be picked and the menu stays open on select. */
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
