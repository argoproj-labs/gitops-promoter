import React, { useEffect, useState } from 'react';
import { timeAgo, formatDate } from '../../../shared/src/utils/util';
import type { Rfc3339DateTime } from '@shared/types/promotion';
import Tooltip from './HistoryView/Tooltip/Tooltip';

interface TimeAgoProps {
  /** RFC 3339 string or `Date`; do not pass pre-formatted `formatDate` output. */
  date: Rfc3339DateTime | Date;
  interval?: number;
}

const TimeAgo: React.FC<TimeAgoProps> = ({ date, interval = 60000 }) => {
  const [, setTick] = useState(0);

  useEffect(() => {
    const timer = setInterval(() => setTick((tick) => tick + 1), interval);
    return () => clearInterval(timer);
  }, [interval]);

  const dateString = typeof date === 'string' ? date : date.toISOString();
  return (
    <Tooltip label={formatDate(dateString)}>
      <span>{timeAgo(dateString)}</span>
    </Tooltip>
  );
};

export default TimeAgo;
