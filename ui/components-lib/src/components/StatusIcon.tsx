import React from 'react';
import {
  FaCircleNotch,
  FaCheckCircle,
  FaTimesCircle,
  FaHeart,
  FaHeartBroken
} from 'react-icons/fa';
import './StatusIcon.scss';

export type StatusType = 'promoted' | 'pending' | 'failure' | 'unknown' | 'success';

export type IconType = 'status' | 'health';

export interface StatusIconProps {
  phase: StatusType;
  type?: IconType;
  size?: 'small' | 'medium' | 'large';
  className?: string;
}

// Active and Inactive Status utility function
export const statusLabel = (phase: StatusType): string => {
  return phase === 'promoted' || phase === 'success' ? 'Active' : 'Inactive';
};

// Get icon component based on phase and type
const getIconComponent = (phase: StatusType, type: IconType) => {
  if (type === 'status') {
    switch (phase) {
      case 'pending':
        return FaCircleNotch;
      case 'promoted':
      case 'success':
        return FaCheckCircle;
      case 'failure':
        return FaTimesCircle;
      case 'unknown':
      default:
        return FaCircleNotch;
    }
  }

  // Health status icons
  switch (phase) {
    case 'pending':
      return FaCircleNotch;
    case 'promoted':
    case 'success':
      return FaHeart;
    case 'failure':
      return FaHeartBroken;
    case 'unknown':
    default:
      return FaHeart;
  }
};

export const StatusIcon: React.FC<StatusIconProps> = ({
  phase,
  type = 'status',
  size = 'medium',
  className = ''
}) => {
  const IconComponent = getIconComponent(phase, type);
  const isSpinning = phase === 'pending';

  const iconClasses = [
    'status-icon',
    `status-${phase}`,
    isSpinning ? 'fa-spin' : '',
    className
  ].filter(Boolean).join(' ');

  return (
    <IconComponent
      className={iconClasses}
      aria-label={`${type === 'health' ? 'Health' : 'Status'}: ${phase}`}
      title={`${type === 'health' ? 'Health' : 'Status'}: ${phase}`}
      role="img"
    />
  );
};
