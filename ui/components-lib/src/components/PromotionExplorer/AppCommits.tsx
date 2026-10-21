import React, { useState } from 'react';
import { FaCube } from 'react-icons/fa';
import { timeAgo, formatDate } from '@shared/utils/util';
import type { RowReference } from './types';
import { safeHttpUrl } from './helpers';
import { CommitBlock } from './CommitBlock';

const AppCommitBlock: React.FC<{
  reference: RowReference;
  trailing?: React.ReactNode;
}> = ({ reference: r, trailing }) => {
  const href = safeHttpUrl(r.url);
  const label = `App commit ${r.shaShort}${r.repoName ? ` in ${r.repoName}` : ''}`;
  const tooltip = r.repoName ? `Repository: ${r.repoName}\nCommit: ${r.shaShort}` : r.shaShort;
  const chip = href ? (
    <a
      className="hp-commit__sha"
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      onClick={(e) => e.stopPropagation()}
      aria-label={`${label}, opens in new tab`}
      title={tooltip}
    >
      {r.shaShort}
    </a>
  ) : (
    <span className="hp-commit__sha" aria-label={label} title={tooltip}>
      {r.shaShort}
    </span>
  );
  return (
    <CommitBlock
      icon={
        <span title="App commit" className="hp-commit__icon-wrap">
          <FaCube className="hp-commit__icon" aria-hidden="true" />
        </span>
      }
      chip={chip}
      title={r.subject ?? '(no subject)'}
      author={
        r.author && (
          <span className="hp-commit__author" title={`Authored by ${r.author}`}>
            {r.author}
          </span>
        )
      }
      time={
        r.date && (
          <span className="hp-commit__time" title={formatDate(r.date)}>
            {timeAgo(r.date)}
          </span>
        )
      }
      trailing={trailing}
    />
  );
};

export const AppCommits: React.FC<{ references: RowReference[] }> = ({ references }) => {
  const [expanded, setExpanded] = useState(false);
  const [first, ...rest] = references;
  if (!first) return null;
  return (
    <div className="hp-row__app" role="group" aria-label="App commit">
      <AppCommitBlock
        reference={first}
        trailing={
          rest.length > 0 && (
            <button
              type="button"
              className="hp-commit__more"
              aria-expanded={expanded}
              onClick={(e) => {
                e.stopPropagation();
                setExpanded((v) => !v);
              }}
            >
              {expanded ? 'Hide' : `+${rest.length} more`}
            </button>
          )
        }
      />
      {expanded &&
        rest.map((r, i) => (
          <AppCommitBlock key={`${r.repoName ?? ''}-${r.shaShort}-${i}`} reference={r} />
        ))}
    </div>
  );
};
