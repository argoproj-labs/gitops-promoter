import React from 'react';
import { FaCheckCircle, FaTimesCircle, FaCircleNotch, FaHeart, FaHeartBroken } from 'react-icons/fa';
import './StatusIcon.scss';


export type StatusType = 'promoted' | 'pending' | 'failure' | 'unknown' | 'success';


export const statusLabel = (phase: StatusType) => {
  switch (phase) {
    case 'promoted': return 'Active';
    case 'success': return 'Active';
    case 'pending': return 'Inactive';
    case 'failure': return 'Inactive';
    default: return 'Inactive';
  }
};



export const StatusIcon: React.FC<{ phase: StatusType; type?: 'status' | 'health' }> = ({ phase, type = 'status' }) => {
  let iconProps = { className: `status-icon status-${phase}` };


  //Promoted status icon
  if (type === 'status') {
    switch (phase) {
      case 'pending': return <FaCircleNotch {...iconProps} className={iconProps.className + ' fa-spin'} />;
      case 'promoted': return <FaCheckCircle {...iconProps} />;
      case 'success': return <FaCheckCircle {...iconProps} />;
      case 'failure': return <FaTimesCircle {...iconProps} />;
      default: return <FaCircleNotch {...iconProps} />;
    }
  } else {


    //Health status icon
    switch (phase) {
      case 'pending': return <FaCircleNotch {...iconProps} className={iconProps.className + ' fa-spin'} />;
      case 'promoted': return <FaHeart {...iconProps} />;
      case 'success': return <FaHeart {...iconProps} />;
      case 'failure': return <FaHeartBroken {...iconProps} />;
      default: return <FaHeart {...iconProps} />;
    }
  }
}; 