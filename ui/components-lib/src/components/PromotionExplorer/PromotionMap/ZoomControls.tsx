import React from 'react';

const ZoomControls: React.FC<{
  zoom: number;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  onReset: () => void;
}> = ({ zoom, onZoomIn, onZoomOut, onFit, onReset }) => (
  <div className="hp-zoom" role="group" aria-label="Zoom controls">
    <button type="button" onClick={onZoomOut} aria-label="Zoom out">
      −
    </button>
    <span className="hp-zoom__value" aria-live="polite">
      {zoom}%
    </span>
    <button type="button" onClick={onZoomIn} aria-label="Zoom in">
      +
    </button>
    <span className="hp-zoom__divider" aria-hidden="true" />
    <button type="button" className="hp-zoom__text" onClick={onFit} aria-label="Fit to view">
      <svg className="hp-zoom__icon" viewBox="0 0 16 16" aria-hidden="true">
        <path d="M2 6V2h4M10 2h4v4M14 10v4h-4M6 14H2v-4" />
      </svg>
      Fit
    </button>
    <button type="button" className="hp-zoom__text" onClick={onReset} aria-label="Reset view">
      Reset
    </button>
  </div>
);

export default ZoomControls;
