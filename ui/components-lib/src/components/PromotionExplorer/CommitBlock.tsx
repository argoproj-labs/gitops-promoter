import React from 'react';

export interface CommitBlockProps {
  icon: React.ReactNode;
  chip: React.ReactNode;
  title: string;
  author?: React.ReactNode;
  time?: React.ReactNode;
  trailing?: React.ReactNode;
  role?: 'group';
  ariaLabel?: string;
}

export const CommitBlock: React.FC<CommitBlockProps> = ({
  icon,
  chip,
  title,
  author,
  time,
  trailing,
  role,
  ariaLabel,
}) => (
  <div className="hp-commit" role={role} aria-label={ariaLabel}>
    <div className="hp-commit__line">
      {icon}
      {chip}
      <span className="hp-commit__title" title={title}>
        {title}
      </span>
      {trailing}
    </div>
    {(author || time) && (
      <div className="hp-commit__meta">
        {author}
        {author && time && (
          <span className="hp-commit__sep" aria-hidden="true">
            ·
          </span>
        )}
        {time}
      </div>
    )}
  </div>
);
