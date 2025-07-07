import './CircularProgress.scss';
import React from 'react';

const CircularProgress = ({ percent }: { percent: number }) => {
  const radius = 15;
  const stroke = 4;
  const normalizedRadius = Math.max(1, radius - stroke / 2);
  const circumference = normalizedRadius * 2 * Math.PI;
  const strokeDashoffset = circumference - (percent / 100) * circumference;

  return (
    <svg
      height={radius * 2}
      width={radius * 2}
      className="circular-progress"
    >
      <circle
        className="circular-progress__bg"
        stroke="#eee"
        fill="transparent"
        strokeWidth={stroke}
        r={normalizedRadius}
        cx={radius}
        cy={radius}
      />
      <circle
        className="circular-progress__fg"
        stroke="#2ecc40"
        fill="transparent"
        strokeWidth={stroke}
        strokeLinecap="round"
        strokeDasharray={circumference + ' ' + circumference}
        style={{ strokeDashoffset, transition: 'stroke-dashoffset 0.5s' }}
        r={normalizedRadius}
        cx={radius}
        cy={radius}
      />
    </svg>
  );
};

export default CircularProgress; 