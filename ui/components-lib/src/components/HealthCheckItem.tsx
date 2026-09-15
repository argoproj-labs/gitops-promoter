import React from 'react';
import { StatusIcon, StatusType } from './StatusIcon';
import { Tooltip } from './Tooltip';
import { Check } from '@shared/types/promotion';
import { useCommitStatusRowPlugin } from '@shared/components/plugins';

export const HealthCheckItem: React.FC<{ check: Check }> = ({ check }) => {
  const Plugin = useCommitStatusRowPlugin(check.kind, check.apiVersion);

  return (
    <Tooltip content={check.description}>
      <div className="health-check-item">
        <StatusIcon phase={check.status as StatusType} type="status" />
        <div className="health-check-body">
          {Plugin && check.manager ? (
            <Plugin.rowHeader check={check} manager={check.manager} />
          ) : check.url ? (
            <a
              href={check.url}
              target="_blank"
              rel="noopener noreferrer"
              className="health-check-name-link"
            >
              {check.name}
            </a>
          ) : (
            <span className="check-name-text">{check.name}</span>
          )}
        </div>
      </div>
    </Tooltip>
  );
};
