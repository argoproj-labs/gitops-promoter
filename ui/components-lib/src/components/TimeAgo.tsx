import React, { useEffect, useState } from 'react';
import { timeAgo, formatDate } from '../../../shared/src/utils/util';

interface TimeAgoProps {
  date: string | Date;
  interval?: number; 
}

const TimeAgo: React.FC<TimeAgoProps> = ({ date, interval = 60000 }) => {
  const [, setTick] = useState(0);

  useEffect(() => {
    const timer = setInterval(() => setTick(tick => tick + 1), interval);
    return () => clearInterval(timer);
  }, [interval]);

  const dateString = typeof date === 'string' ? date : date.toISOString();
  return <span title={formatDate(dateString)}>{timeAgo(dateString)}</span>;
};

export default TimeAgo; 