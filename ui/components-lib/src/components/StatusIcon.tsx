import React from 'react';
import { FaCheckCircle, FaTimesCircle, FaCircleNotch, FaHeart, FaHeartBroken } from 'react-icons/fa';
import './StatusIcon.scss';


export type StatusType = 'success' | 'pending' | 'failure' | 'unknown';


export const statusLabel = (phase: StatusType) => {
  switch (phase) {
    case 'success': return 'Healthy';
    case 'pending': return 'Pending';
    case 'failure': return 'Failed';
    default: return 'Unknown';
  }
};



export const StatusIcon: React.FC<{ phase: StatusType; type?: 'status' | 'health' }> = ({ phase, type = 'status' }) => {
  let iconProps = { className: `status-icon status-${phase}` };


  //Promoted status icon
  if (type === 'status') {
    switch (phase) {
      case 'success': return <FaCheckCircle {...iconProps} />;
      case 'pending': return <FaCircleNotch {...iconProps} className={iconProps.className + ' fa-spin'} />;
      case 'failure': return <FaTimesCircle {...iconProps} />;

      default: return <FaCircleNotch {...iconProps} />;
    }
  } else {


    //Health status icon
    switch (phase) {

      case 'success': return <FaHeart {...iconProps} />;
      case 'pending': return <FaCircleNotch {...iconProps} className={iconProps.className + ' fa-spin'} />;
      case 'failure': return <FaHeartBroken {...iconProps} />;
      default: return <FaHeart {...iconProps} />;
    }
  }
}; 