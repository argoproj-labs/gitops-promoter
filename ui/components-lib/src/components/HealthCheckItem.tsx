import React from 'react';
import { StatusIcon, StatusType } from './StatusIcon';
import { Tooltip } from './Tooltip';
import { Check } from '@shared/types/promotion';
import { useCommitStatusRowPlugin, PluginErrorBoundary } from '@shared/components/plugins';

const DefaultCheckRow: React.FC<{ check: Check }> = ({ check }) =>
  check.url ? (
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
  );

export const HealthCheckItem: React.FC<{ check: Check }> = ({ check }) => {
  const Plugin = useCommitStatusRowPlugin(
    check.kind,
    check.apiVersion,
    check.manager?.metadata?.annotations,
  );

  return (
    <Tooltip content={check.description}>
      <div className="health-check-item">
        <StatusIcon phase={check.status as StatusType} type="status" />
        <div className="health-check-body">
          {Plugin && check.manager ? (
            <PluginErrorBoundary
              key={check.kind}
              pluginKind={check.kind}
              fallback={<DefaultCheckRow check={check} />}
            >
              <Plugin.rowHeader check={check} manager={check.manager} />
            </PluginErrorBoundary>
          ) : (
            <DefaultCheckRow check={check} />
          )}
        </div>
      </div>
    </Tooltip>
  );
};
