import React, { useEffect, useState } from 'react';
import Select, { SingleValue } from 'react-select';
import PromotionExplorer from '@components-lib/components/PromotionExplorer/PromotionExplorer';
import type { CellSelection } from '@components-lib/components/PromotionExplorer/PromotionExplorer';
import {
  formatEnvSelection,
  parseEnvSelection,
} from '@components-lib/components/PromotionExplorer/helpers';
import { PromotionStrategy } from '@shared/types/promotion';
import type { PromotionStrategyDetails } from '@shared/types/view';
import { mergeCommitStatusManagers } from '@shared/utils/PSData';
import { environmentsFromBundle } from '@shared/utils/environments';
import type { CommitStatusManagerBundle } from '@shared/utils/PSData';
import { AppViewComponentProps } from '@shared/types/extension';
import { sortStrategyCommitStatuses } from '@shared/utils/util';
import './StrategyDropdown.scss';

const GROUP = 'view.promoter.argoproj.io';
const KIND = 'PromotionStrategyDetails';
const PARAM = 'promotionstrategy';
const STORAGE_PREFIX = 'gitops-promoter:lastStrategy:';

interface StrategyItem {
  promotionStrategy: PromotionStrategy;
}

function managersFromBundle(bundle: PromotionStrategyDetails): CommitStatusManagerBundle {
  return {
    timedCommitStatuses: bundle.timedCommitStatuses,
    gitCommitStatuses: bundle.gitCommitStatuses,
    scheduledCommitStatuses: bundle.scheduledCommitStatuses,
    argoCDCommitStatuses: bundle.argoCDCommitStatuses,
    webRequestCommitStatuses: bundle.webRequestCommitStatuses,
    dependentsSuccessfulCommitStatuses: bundle.dependentsSuccessfulCommitStatuses,
  };
}

function bundleToItem(bundle: PromotionStrategyDetails): StrategyItem {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromBundle(
    ps.spec,
    bundle.changeTransferPolicies ?? [],
    bundle.changeTransferPolicyHistories ?? [],
    bundle.restoreActiveCommits ?? [],
  );
  const promotionStrategy = {
    ...ps,
    metadata: {
      ...ps.metadata,
      name: bundle.metadata.name,
      namespace: bundle.metadata.namespace,
    },
    status: { ...ps.status, environments },
  } as PromotionStrategy;
  sortStrategyCommitStatuses(promotionStrategy);
  return {
    promotionStrategy: mergeCommitStatusManagers(promotionStrategy, managersFromBundle(bundle)),
  };
}

interface SelectOption {
  value: string;
  label: string;
}

const getParam = (): string => {
  const params = new URLSearchParams(window.location.search);
  return params.get(PARAM) || '';
};

const setParam = (name: string) => {
  const url = new URL(window.location.href);
  if (name) {
    url.searchParams.set(PARAM, name);
  } else {
    url.searchParams.delete(PARAM);
  }
  window.history.replaceState(null, '', url.toString());
};

const COMMIT_PARAM = 'psCommit';
const ENV_PARAM = 'psEnv';
const NODE_PARAM = 'psNode';

const getSelectionFromUrl = (): CellSelection | null => {
  const params = new URLSearchParams(window.location.search);
  const rowId = params.get(COMMIT_PARAM);
  const branch = params.get(ENV_PARAM);
  return rowId && branch ? { rowId, branch } : null;
};

const setSelectionInUrl = (selection: CellSelection | null) => {
  const url = new URL(window.location.href);
  if (selection) {
    url.searchParams.set(COMMIT_PARAM, selection.rowId);
    url.searchParams.set(ENV_PARAM, selection.branch);
  } else {
    url.searchParams.delete(COMMIT_PARAM);
    url.searchParams.delete(ENV_PARAM);
  }
  window.history.replaceState(null, '', url.toString());
};

const getSelectedEnvsFromUrl = (): string[] =>
  parseEnvSelection(new URLSearchParams(window.location.search).get(NODE_PARAM));

const setSelectedEnvsInUrl = (branches: string[]) => {
  const url = new URL(window.location.href);
  const value = formatEnvSelection(branches);
  if (value) {
    url.searchParams.set(NODE_PARAM, value);
  } else {
    url.searchParams.delete(NODE_PARAM);
  }
  window.history.replaceState(null, '', url.toString());
};

const storageKey = (appNamespace: string, appName: string) =>
  `${STORAGE_PREFIX}${appNamespace}/${appName}`;

const getStored = (appNamespace: string, appName: string): string => {
  try {
    return window.localStorage.getItem(storageKey(appNamespace, appName)) || '';
  } catch {
    return '';
  }
};

const setStored = (appNamespace: string, appName: string, name: string) => {
  try {
    const key = storageKey(appNamespace, appName);
    if (name) {
      window.localStorage.setItem(key, name);
    } else {
      window.localStorage.removeItem(key);
    }
  } catch {
    // localStorage may be unavailable (privacy mode); fall through silently.
  }
};

const strategyKey = (s: PromotionStrategy) => `${s.metadata.namespace}/${s.metadata.name}`;

const AppViewExtension = ({ application, tree }: AppViewComponentProps) => {
  const [strategies, setStrategies] = useState<StrategyItem[]>([]);
  const [selectedKey, setSelectedKey] = useState<string>(
    () => getParam() || getStored(application.metadata.namespace, application.metadata.name),
  );
  const [fetchError, setFetchError] = useState<string | null>(null);

  useEffect(() => {
    const appName = application.metadata.name;
    const appNamespace = application.metadata.namespace;

    const strategyNodes = (tree.nodes ?? []).filter(
      (node) => node.group === GROUP && node.kind === KIND,
    );

    if (strategyNodes.length === 0) {
      setFetchError('No PromotionStrategy resources found');
      setStrategies([]);
      setSelectedKey('');
      setParam('');
      setStored(appNamespace, appName, '');
      return;
    }

    setFetchError(null);
    Promise.all(
      strategyNodes.map(async (node) => {
        const params = new URLSearchParams({
          appNamespace,
          namespace: node.namespace,
          resourceName: node.name,
          version: node.version || '',
          kind: KIND,
          group: GROUP,
        });
        const response = await fetch(`/api/v1/applications/${appName}/resource?${params}`);
        if (!response.ok) {
          let errorText = '';
          try {
            errorText = await response.text();
          } catch {
            // ignore errors while reading error body
          }
          const messageParts = [
            `Request failed with status ${response.status} ${response.statusText}`,
            errorText && `body: ${errorText}`,
          ].filter(Boolean);
          throw new Error(messageParts.join(' - '));
        }
        const data: { manifest: string } = await response.json();
        return bundleToItem(JSON.parse(data.manifest) as PromotionStrategyDetails);
      }),
    )
      .then((parsed) => {
        setStrategies(parsed);
        const keys = parsed.map((item) => strategyKey(item.promotionStrategy));
        const fromUrl = getParam();
        const fromStored = getStored(appNamespace, appName);
        const initial =
          (keys.includes(fromUrl) && fromUrl) ||
          (keys.includes(fromStored) && fromStored) ||
          keys[0];
        setSelectedKey(initial);
        setParam(initial);
        setStored(appNamespace, appName, initial);
      })
      .catch((err) => {
        const errorMessage = err instanceof Error ? err.message : String(err);
        setFetchError('Failed to load PromotionStrategy: ' + errorMessage);
        setStrategies([]);
        setSelectedKey('');
        setParam('');
        setStored(appNamespace, appName, '');
      });
  }, [application.metadata.name, application.metadata.namespace, tree]);

  if (strategies.length === 0) {
    if (fetchError) {
      return <div>{fetchError}</div>;
    }
    return <div>Loading...</div>;
  }

  const selected = strategies.find((s) => strategyKey(s.promotionStrategy) === selectedKey);

  const hasDuplicateNames =
    new Set(strategies.map((s) => s.promotionStrategy.metadata.name)).size < strategies.length;

  const options: SelectOption[] = strategies.map((s) => ({
    value: strategyKey(s.promotionStrategy),
    label: hasDuplicateNames
      ? `${s.promotionStrategy.metadata.name} (${s.promotionStrategy.metadata.namespace})`
      : s.promotionStrategy.metadata.name,
  }));

  return (
    <div className="extension-container">
      {strategies.length > 1 && (
        <div className="gp-controls">
          <div className="strategy-dropdown-wrapper">
            <Select<SelectOption>
              classNamePrefix="strategy-dropdown"
              options={options}
              placeholder="Select a PromotionStrategy"
              value={options.find((opt) => opt.value === selectedKey) || null}
              menuPortalTarget={typeof document !== 'undefined' ? document.body : null}
              styles={{ menuPortal: (base) => ({ ...base, zIndex: 2000 }) }}
              onChange={(option: SingleValue<SelectOption>) => {
                const key = option ? option.value : '';
                setSelectedKey(key);
                setParam(key);
                setSelectionInUrl(null);
                setSelectedEnvsInUrl([]);
                setStored(application.metadata.namespace, application.metadata.name, key);
              }}
            />
          </div>
        </div>
      )}
      {selected && (
        <div className="gp-explorer-wrapper">
          <PromotionExplorer
            key={selectedKey}
            strategy={selected.promotionStrategy}
            initialSelection={getSelectionFromUrl()}
            onSelectionChange={setSelectionInUrl}
            initialSelectedEnvs={getSelectedEnvsFromUrl()}
            onSelectedEnvsChange={setSelectedEnvsInUrl}
          />
        </div>
      )}
    </div>
  );
};

export default AppViewExtension;
