import React from 'react';
import { FiChevronDown, FiChevronUp } from 'react-icons/fi';
import { useCommitStatusRowPlugin, PluginErrorBoundary } from '@shared/components/plugins';
import type { Check } from '@shared/types/promotion';
import type { HealthKey } from '../types';
import { HEALTH_LABELS } from '../presentation';
import { StatusIcon, StatusType } from '../../StatusIcon';

const DefaultCheckRow: React.FC<{ check: Check }> = ({ check }) => (
  <>
    <span className="hp-sr-only">
      {HEALTH_LABELS[check.status as HealthKey] ?? HEALTH_LABELS.unknown}:{' '}
    </span>
    <span className="hp-drawer__check-key">{check.name}</span>
    {check.description && <span className="hp-drawer__check-desc">{check.description}</span>}
    {check.url && (
      <a
        href={check.url}
        target="_blank"
        rel="noreferrer"
        className="hp-drawer__check-link"
        aria-label={`View details for ${check.name}, opens in new tab`}
      >
        View details
      </a>
    )}
  </>
);

export const DrawerCheckItem: React.FC<{
  check: Check;
  isExpanded: boolean;
  onToggleExpanded: () => void;
}> = ({ check, isExpanded, onToggleExpanded }) => {
  const Plugin = useCommitStatusRowPlugin(
    check.kind,
    check.apiVersion,
    check.manager?.metadata?.annotations,
  );
  const manager = Plugin ? check.manager : undefined;
  const RowContent = manager ? Plugin?.rowContent : undefined;
  const panelId = `hp-drawer-check-panel-${check.name}`;
  const phase: StatusType = HEALTH_LABELS[check.status as HealthKey]
    ? (check.status as StatusType)
    : 'unknown';

  return (
    <li className="hp-drawer__check-item">
      <div className={`hp-drawer__check hp-drawer__check--${check.status}`}>
        <span className="hp-drawer__check-icon" aria-hidden="true">
          <StatusIcon phase={phase} type="status" />
        </span>
        {RowContent && (
          <button
            type="button"
            className="hp-drawer__check-toggle"
            aria-expanded={isExpanded}
            aria-controls={panelId}
            onClick={onToggleExpanded}
          >
            <span className="hp-sr-only">
              {isExpanded ? 'Hide details for ' : 'Show details for '}
              {check.name}
            </span>
            {isExpanded ? <FiChevronUp aria-hidden="true" /> : <FiChevronDown aria-hidden="true" />}
          </button>
        )}
        {Plugin && manager ? (
          <PluginErrorBoundary
            key={check.kind}
            pluginKind={check.kind}
            fallback={<DefaultCheckRow check={check} />}
          >
            <Plugin.rowHeader check={check} manager={manager} />
          </PluginErrorBoundary>
        ) : (
          <DefaultCheckRow check={check} />
        )}
      </div>
      {RowContent && manager && (
        // Stays mounted while collapsed (hidden is CSS-only, not an unmount), so a
        // plugin's timers/effects keep running even when the panel isn't visible.
        <div id={panelId} className="hp-drawer__check-panel" hidden={!isExpanded}>
          <PluginErrorBoundary key={check.kind} pluginKind={check.kind} fallback={null}>
            <RowContent check={check} manager={manager} />
          </PluginErrorBoundary>
        </div>
      )}
    </li>
  );
};
