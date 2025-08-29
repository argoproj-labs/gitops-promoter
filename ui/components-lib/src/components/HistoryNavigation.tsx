import React, { useCallback, useMemo } from 'react';
import { getHealthStatus } from '@shared/utils/getStatus';
import { timeAgo } from '@shared/utils/util';
import type { History, CommitStatus } from '@shared/types/promotion';
import './HistoryNavigation.scss';


interface Check {
  name: string;
  status: string;
  details?: string;
  url?: string;
}

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
  const getStatusColor = useCallback((commitStatuses: CommitStatus[] = []): string => {
    const checks: Check[] = commitStatuses.map(cs => ({
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
  }, []);

  const handleHistorySelect = useCallback((index: number) => {
    onHistorySelect(index);
  }, [onHistorySelect]);

  // Get current status from the first history entry or default to unknown
  const currentStatusColor = useMemo(() => {
    return history.length > 0 ?
      getStatusColor(history[0]?.active?.commitStatuses) : 'unknown';
  }, [history, getStatusColor]);

  // Calculate position indicator
  const positionInfo = useMemo(() => {
    const totalItems = history.length;
    return {
      text: `${currentIndex + 1} of ${totalItems}`,
      current: currentIndex + 1,
      total: totalItems
    };
  }, [currentIndex, history.length]);

  const formatHistoryItemDate = useCallback((date: string | null | undefined) => {
    if (!date) return { timeAgo: '', formatted: '' };

    return {
      timeAgo: timeAgo(date),
      formatted: new Date(date).toLocaleDateString()
    };
  }, []);

  if (history.length === 0) {
    return null;
  }

  return (
    <div className="history-navigation">
      {/* Current entry (history @ 0) */}
      <button
        className={`history-item current-item ${currentIndex === 0 ? 'active' : ''}`}
        onClick={() => handleHistorySelect(0)}
        title="Current state"
        type="button"
        aria-label="Current state"
        aria-pressed={currentIndex === 0}
      >
        <span
          className={`history-item-inner ${currentStatusColor}`}
          aria-hidden="true"
        />
      </button>
      
      {/* Historical entries */}
      {history.slice(1).map((entry, index) => {
        const historyIndex = index + 1;
        const isActive = historyIndex === currentIndex;
        const date = entry.active?.dry?.commitTime;
        const dateInfo = formatHistoryItemDate(date);
        const statusColor = getStatusColor(entry.active?.commitStatuses);
        
        const title = date
          ? `${dateInfo.formatted} (${dateInfo.timeAgo})`
          : `History entry ${historyIndex}`;

        return (
          <button
            key={`history-${historyIndex}`}
            className={`history-item ${isActive ? 'active' : ''} status-${statusColor}`}
            onClick={() => handleHistorySelect(historyIndex)}
            title={title}
            type="button"
            aria-label={`History entry ${historyIndex}: ${title}`}
            aria-pressed={isActive}
          >
            <span
              className={`history-item-inner ${statusColor}`}
              aria-hidden="true"
            />
          </button>
        );
      })}
      
      {/* Position indicator */}
      <div
        className="history-position"
        aria-live="polite"
        aria-label={`Viewing entry ${positionInfo.current} of ${positionInfo.total}`}
      >
        {positionInfo.text}
      </div>
    </div>
  );
};

export default HistoryNavigation;
