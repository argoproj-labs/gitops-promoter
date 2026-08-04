import React from 'react';
import { FaTimesCircle, FaCircleNotch, FaHeart, FaHeartBroken } from 'react-icons/fa';
import { BsCheckCircle, BsDashCircleDotted } from 'react-icons/bs';
import './StatusIcon.scss';

export type StatusType = 'promoted' | 'pending' | 'failure' | 'unknown' | 'success';

// Active and Inactive Status
export const statusLabel = (phase: StatusType) => {
  return phase === 'promoted' || phase === 'success' ? 'Active' : 'Inactive';
};

export const StatusIcon: React.FC<{ phase: StatusType; type?: 'status' | 'health' }> = ({
  phase,
  type = 'status',
}) => {
  const iconClass = `status-icon status-${phase}`;

  // Promoted Status
  if (type === 'status') {
    switch (phase) {
      case 'pending':
        return <FaCircleNotch className={iconClass + ' fa-spin'} />;
      case 'promoted':
      case 'success':
        return <BsCheckCircle className={iconClass} />;
      case 'failure':
        return <FaTimesCircle className={iconClass} />;
      default:
        return <BsDashCircleDotted className={iconClass} />;
    }
  }

  // Health status
  switch (phase) {
    case 'pending':
      return <FaCircleNotch className={iconClass + ' fa-spin'} />;
    case 'promoted':
    case 'success':
      return <FaHeart className={iconClass} />;
    case 'failure':
      return <FaHeartBroken className={iconClass} />;
    default:
      return <FaHeart className={iconClass} />;
  }
};
