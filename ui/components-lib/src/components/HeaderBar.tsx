import React from 'react';
import './HeaderBar.scss';

interface HeaderBarProps {
  name: string;
  link?: string;
  className?: string;
  level?: 1 | 2 | 3 | 4 | 5 | 6;
}

const HeaderBar: React.FC<HeaderBarProps> = ({
  name,
  link,
  className = '',
  level = 2
}) => {
  const HeaderTag = `h${level}` as keyof JSX.IntrinsicElements;

  const headerClasses = [
    'header-bar',
    link ? 'header-bar--clickable' : '',
    className
  ].filter(Boolean).join(' ');

  const content = (
    <HeaderTag className="header-bar__title">
      {name}
    </HeaderTag>
  );

  const containerClasses = [
    'header-bar__container',
    link ? 'header-bar__container--linked' : ''
  ].filter(Boolean).join(' ');

  return (
    <header className={headerClasses}>
      <div className={containerClasses}>
        {link ? (
          <a
            href={link}
            className="header-bar__link"
            aria-label={`Navigate to ${name}`}
          >
            {content}
          </a>
        ) : (
          content
        )}
      </div>
    </header>
  );
};

export default HeaderBar;
