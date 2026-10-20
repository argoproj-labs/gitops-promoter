import React from 'react';
import type { ExplorerNode } from '../explorerModel';
import { nodeModifiers, type NodeVisual } from '../graphFocus';
import StatusBadge from './StatusBadge';

const NodeCard: React.FC<{
  node: ExplorerNode;
  visual: NodeVisual;
  selected: boolean;
  delay: number;
  onSelect: (_id: string, _additive: boolean) => void;
}> = ({ node, visual, selected, delay, onSelect }) => (
  <button
    type="button"
    className={['hp-node', `hp-node--${node.status}`, ...nodeModifiers(visual)]
      .concat(selected ? ['hp-node--pinned'] : [])
      .join(' ')}
    style={{
      left: node.x,
      top: node.y,
      width: node.width,
      height: node.height,
      animationDelay: `${delay}ms`,
    }}
    onClick={(event) => onSelect(node.id, event.shiftKey)}
    aria-pressed={selected}
    aria-label={`${selected ? 'Deselect' : 'Select'} ${node.branch}`}
    data-node-id={node.id}
  >
    <span className="hp-node__top">
      <span className="hp-node__kicker">{node.kicker}</span>
      <StatusBadge status={node.status} />
    </span>
    <span className="hp-node__title">{node.title}</span>
    <span className="hp-node__hash">{node.hash ?? ''}</span>
  </button>
);

export default NodeCard;
