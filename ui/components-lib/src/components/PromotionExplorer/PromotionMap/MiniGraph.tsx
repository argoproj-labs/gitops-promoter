import React from 'react';
import type { ExplorerEdge, ExplorerNode } from '../explorerModel';

const MINI_PAD = 40;

const MiniGraph: React.FC<{
  nodes: ExplorerNode[];
  edges: ExplorerEdge[];
  width: number;
  height: number;
}> = ({ nodes, edges, width, height }) => (
  <svg
    className="hp-minigraph"
    viewBox={`${-MINI_PAD} ${-MINI_PAD} ${width + MINI_PAD * 2} ${height + MINI_PAD * 2}`}
    preserveAspectRatio="xMidYMid meet"
    aria-hidden="true"
    data-testid="hp-minigraph"
  >
    {edges.map((edge) => (
      <path key={edge.id} d={edge.d} />
    ))}
    {nodes.map((node) => (
      <rect
        key={node.id}
        className={`hp-minigraph__node hp-minigraph__node--${node.status}`}
        x={node.x}
        y={node.y}
        width={node.width}
        height={node.height}
        rx={10}
      />
    ))}
  </svg>
);

export default MiniGraph;
