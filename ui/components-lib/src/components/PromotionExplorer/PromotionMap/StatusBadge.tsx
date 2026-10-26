import React from 'react';
import { NODE_STATUS_LABELS, type NodeStatus } from '../explorerModel';

const StatusBadge: React.FC<{ status: NodeStatus; size?: 'sm' | 'md' }> = ({
  status,
  size = 'sm',
}) => (
  <span className={`hp-status hp-status--${status} hp-status--${size}`}>
    {NODE_STATUS_LABELS[status]}
  </span>
);

export default StatusBadge;
