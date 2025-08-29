import React, { useState, useEffect, useMemo } from 'react';
import { timeAgo, formatDate } from '@shared/utils/util';

interface TimeAgoProps {
  date: string | Date;
  interval?: number;
  className?: string;
  showTooltip?: boolean;
}

const TimeAgo: React.FC<TimeAgoProps> = ({
  date,
  interval = 60000,
  className = '',
  showTooltip = true
}) => {
  const [tick, setTick] = useState(0);

  // Convert date to string format for consistency
  const dateString = useMemo(() => {
    return typeof date === 'string' ? date : date.toISOString();
  }, [date]);

  const formattedDate = useMemo(() => {
    return formatDate(dateString);
  }, [dateString]);

  const timeAgoText = useMemo(() => {
    // Force re-computation when tick changes
    return timeAgo(dateString) + (tick >= 0 ? '' : '');
  }, [dateString, tick]);

  useEffect(() => {
    const timer = setInterval(() => {
      setTick(prevTick => prevTick + 1);
    }, interval);

    return () => {
      clearInterval(timer);
    };
  }, [interval]);

  const componentClasses = [
    'time-ago',
    className
  ].filter(Boolean).join(' ');

  return (
    <time
      className={componentClasses}
      dateTime={dateString}
      title={showTooltip ? formattedDate : undefined}
      aria-label={`${timeAgoText}, ${formattedDate}`}
    >
      {timeAgoText}
    </time>
  );
};

export default TimeAgo;
