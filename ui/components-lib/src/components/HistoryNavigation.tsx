import React from 'react';
import { timeAgo } from '@shared/utils/util';
import { getHealthStatus } from '@shared/utils/getStatus';
import type { History, CommitStatus } from '@shared/types/promotion';
import './HistoryNavigation.scss';


interface HistoryNavigationProps {
  history: History[];
  currentIndex: number;
  onHistorySelect: (index: number) => void;
}

const HistoryNavigation: React.FC<HistoryNavigationProps> = ({
  history,
  currentIndex,
  onHistorySelect
}) => {


  const getStatusColor = (commitStatuses: CommitStatus[] = []) => {
    const checks = commitStatuses.map(cs => ({
      name: cs.key,
      status: cs.phase || 'unknown',
      details: cs.details,
      url: cs.url
    }));

    
    const status = getHealthStatus(checks);
    switch (status) {
      case 'success':
        return 'success';
      case 'failure':
        return 'failure';
      case 'pending':
        return 'pending';
      default:
        return 'unknown';
    }
  };

  // Get current status from the first history entry or default to unknown
  const currentStatusColor = history.length > 0 ? 
    getStatusColor(history[0]?.active?.commitStatuses) : 'unknown';

  // Calculate position indicator
  const totalItems = history.length;
  const positionText = `${currentIndex + 1} of ${totalItems}`;

  return (
    <div className="history-navigation">


      {/* Current entry history @ 0*/}
      <button
        className={`history-item current-item ${currentIndex === 0 ? 'active' : ''}`}
        onClick={() => onHistorySelect(0)}
        title="Current"
      >
        <span className={`history-item-inner ${currentStatusColor}`} />
      </button>
      
      {/* History */}
      {history.slice(1).map((entry, index) => {
        const isActive = index + 1 === currentIndex;
        const date = entry.active?.dry?.commitTime;
        const timeAgoText = date ? timeAgo(date) : '';
        const formattedDate = date ? new Date(date).toLocaleDateString() : '';
        const statusColor = getStatusColor(entry.active?.commitStatuses);
        
        return (
          <button
            key={index}
            className={`history-item ${isActive ? 'active' : ''} status-${statusColor}`}
            onClick={() => onHistorySelect(index + 1)}
            title={`${formattedDate} (${timeAgoText})`}
          >
            <span className={`history-item-inner ${statusColor}`} />
          </button>
        );
      })}
      
      {/* Position indicator */}
      <div className="history-position">
        {positionText}
      </div>
    </div>
  );
};

export default HistoryNavigation;
