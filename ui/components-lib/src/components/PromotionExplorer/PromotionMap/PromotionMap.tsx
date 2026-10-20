import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { ExplorerModel } from '../explorerModel';
import { edgeVisual, nodeVisual, type CommitPath } from '../graphFocus';
import { usePanZoom } from '../usePanZoom';
import { MAP_COLLAPSED_HEIGHT, preferredMapHeight, resolveSplitResize } from '../viewport';
import MiniGraph from './MiniGraph';
import NodeCard from './NodeCard';
import SplitHandle from './SplitHandle';
import ZoomControls from './ZoomControls';

const CHROME_SELECTOR = '.hp-node, .hp-zoom, .hp-map__chrome, .hp-map__thumb';

const PromotionMap: React.FC<{
  model: ExplorerModel;
  selectedNodes: readonly string[];
  path: CommitPath | null;
  defaultExpanded?: boolean;
  onSelectNode: (_id: string, _additive: boolean) => void;
  onClearSelection: () => void;
}> = ({ model, selectedNodes, path, defaultExpanded = false, onSelectNode, onClearSelection }) => {
  const { nodes, edges, layout } = model;
  const content = useMemo(
    () => ({ width: layout.width, height: layout.height }),
    [layout.width, layout.height],
  );

  const canvasRef = useRef<HTMLDivElement>(null);
  const fitAfterLayout = useRef<number | null>(null);
  const [expanded, setExpanded] = useState(defaultExpanded);
  const [animating, setAnimating] = useState(false);
  const [canvasHeight, setCanvasHeight] = useState<number | null>(() =>
    defaultExpanded ? preferredMapHeight(content.height, window.innerHeight) : null,
  );
  const [edgesReady, setEdgesReady] = useState(false);

  const view = usePanZoom({
    canvasRef,
    content,
    wheelEnabled: expanded,
    ignoreSelector: CHROME_SELECTOR,
  });
  const { fit, isManualZoom } = view;

  useEffect(() => {
    fit();
    const timer = window.setTimeout(() => setEdgesReady(true), 720);
    return () => window.clearTimeout(timer);
  }, [fit]);

  useLayoutEffect(() => {
    if (fitAfterLayout.current === null || !expanded) return;
    const target = fitAfterLayout.current;
    fitAfterLayout.current = null;
    fit(target);
  }, [canvasHeight, expanded, fit]);

  const expand = useCallback(
    (height?: number) => {
      fitAfterLayout.current = preferredMapHeight(content.height, window.innerHeight);
      setAnimating(height === undefined);
      setExpanded(true);
      setCanvasHeight(height ?? preferredMapHeight(content.height, window.innerHeight));
    },
    [content.height],
  );

  const collapse = useCallback(() => {
    setAnimating(true);
    setExpanded(false);
    setCanvasHeight(MAP_COLLAPSED_HEIGHT);
  }, []);

  const onSplitResize = useCallback(
    (height: number | null) => {
      const resolved = resolveSplitResize(height);
      if (resolved.kind === 'collapse') {
        collapse();
        return;
      }
      if (expanded) {
        setAnimating(false);
        setCanvasHeight(resolved.height);
      } else {
        expand(resolved.height);
      }
    },
    [collapse, expand, expanded],
  );

  const onSplitResizeEnd = useCallback(() => {
    if (!expanded || isManualZoom()) return;
    fit();
  }, [expanded, fit, isManualZoom]);

  const inspecting = path !== null;
  const className = [
    'hp-map',
    expanded ? 'hp-map--expanded' : 'hp-map--collapsed',
    animating ? 'hp-map--animating' : '',
    view.panning ? 'hp-map--panning' : '',
    inspecting ? 'hp-map--inspecting' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <>
      <div
        ref={canvasRef}
        className={className}
        role="region"
        aria-label="Promotion map"
        style={canvasHeight ? { height: canvasHeight, minHeight: canvasHeight } : undefined}
        onTransitionEnd={(event) => {
          if (event.propertyName !== 'height') return;
          setAnimating(false);
          if (!expanded) setCanvasHeight(null);
        }}
        {...view.canvasHandlers}
        onClick={(event) => {
          if ((event.target as HTMLElement).closest(CHROME_SELECTOR)) return;
          onClearSelection();
        }}
      >
        <button
          type="button"
          className={`hp-map__thumb${expanded ? ' hp-map__thumb--expanded' : ''}`}
          aria-expanded={expanded}
          aria-label={expanded ? 'Collapse promotion map' : 'Open promotion map'}
          onClick={() => (expanded ? collapse() : expand())}
        >
          <svg className="hp-map__thumb-icon" viewBox="0 0 16 16" aria-hidden="true">
            {expanded ? (
              <path d="M3 13h10M5 6.5 L8 10 L11 6.5" />
            ) : (
              <path d="M3 13h10M5 10 L8 6.5 L11 10" />
            )}
          </svg>
          <span>{expanded ? 'Collapse promotion map' : 'Open promotion map'}</span>
          {!expanded && (
            <MiniGraph nodes={nodes} edges={edges} width={layout.width} height={layout.height} />
          )}
        </button>

        <div className="hp-map__chrome" aria-hidden={!expanded}>
          <div className="hp-map__heading">
            <strong>Promotion map</strong>
            <span>Click a node to filter the history. Shift+click to select several.</span>
          </div>
        </div>

        <div
          className="hp-map__graph"
          aria-hidden={!expanded}
          style={{
            width: layout.width,
            height: layout.height,
            marginLeft: -layout.width / 2,
            marginTop: -layout.height / 2,
            transform: `translate(${view.offset.x}px, ${view.offset.y}px) scale(${view.zoom / 100})`,
          }}
          data-testid="hp-map-graph"
        >
          <svg
            className={`hp-map__edges${edgesReady ? ' hp-map__edges--ready' : ''}`}
            width={layout.width}
            height={layout.height}
            viewBox={`0 0 ${layout.width} ${layout.height}`}
            aria-hidden="true"
          >
            {edges.map((edge) => {
              const mode = edgeVisual(edge.from, edge.to, path);
              return (
                <path
                  key={edge.id}
                  className={`hp-edge${mode ? ` hp-edge--${mode}` : ''}`}
                  d={edge.d}
                  data-edge-id={edge.id}
                />
              );
            })}
          </svg>
          {nodes.map((node, index) => (
            <NodeCard
              key={node.id}
              node={node}
              visual={nodeVisual(node.id, path)}
              selected={selectedNodes.includes(node.id)}
              delay={index * 18}
              onSelect={onSelectNode}
            />
          ))}
        </div>

        <ZoomControls
          zoom={view.zoom}
          onZoomIn={view.zoomIn}
          onZoomOut={view.zoomOut}
          onFit={() => view.fit()}
          onReset={view.reset}
        />
      </div>
      <SplitHandle target={canvasRef} onResize={onSplitResize} onResizeEnd={onSplitResizeEnd} />
    </>
  );
};

export default PromotionMap;
