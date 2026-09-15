import React, { useEffect, useState } from 'react';
import Select, { SingleValue } from 'react-select';
import Card from '@components-lib/components/Card';
import HistoryView from '@components-lib/components/HistoryView/HistoryView';
import type { CellSelection } from '@components-lib/components/HistoryView/HistoryView';
import { PromotionStrategy } from '@shared/types/promotion';
import type { ChangeTransferPolicy, PromotionStrategyDetails } from '@shared/types/view';
import type { Environment } from '@shared/types/promotion';
import { mergeCommitStatusManagers } from '@shared/utils/PSData';
import type { CommitStatusManagerBundle } from '@shared/utils/PSData';
import { AppViewComponentProps } from '@shared/types/extension';
import { sortStrategyCommitStatuses } from '@shared/utils/util';
import {
  EXTENSION_HISTORY_PARAMS,
  EXTENSION_SELECTION_PARAMS,
  readHistoryViewState,
  readSelection,
  writeHistoryViewState,
  writeSelection,
} from '@shared/utils/deepLink';
import type { HistoryViewState } from '@shared/utils/deepLink';
import './StrategyDropdown.scss';

type ViewMode = 'card' | 'history';

const GROUP = 'view.promoter.argoproj.io';
const KIND = 'PromotionStrategyDetails';
const PARAM = 'promotionstrategy';
const STORAGE_PREFIX = 'gitops-promoter:lastStrategy:';

interface StrategyItem {
  promotionStrategy: PromotionStrategy;
}

function environmentsFromCTPs(
  spec: PromotionStrategy['spec'],
  ctps: ChangeTransferPolicy[],
): Environment[] {
  const byBranch = new Map<string, ChangeTransferPolicy>();
  for (const ctp of ctps) {
    const branch = ctp.spec?.activeBranch;
    if (branch) byBranch.set(branch, ctp);
  }

  return spec.environments.map((env) => {
    const status = byBranch.get(env.branch)?.status ?? {};
    return {
      branch: env.branch,
      active: status.active ?? { dry: {}, hydrated: {} },
      proposed: status.proposed ?? { dry: {}, hydrated: {} },
      pullRequest: status.pullRequest,
      history: status.history,
      lastHealthyDryShas: [],
    };
  });
}

function managersFromBundle(bundle: PromotionStrategyDetails): CommitStatusManagerBundle {
  return {
    timedCommitStatuses: bundle.timedCommitStatuses,
    gitCommitStatuses: bundle.gitCommitStatuses,
    scheduledCommitStatuses: bundle.scheduledCommitStatuses,
    argoCDCommitStatuses: bundle.argoCDCommitStatuses,
    webRequestCommitStatuses: bundle.webRequestCommitStatuses,
  };
}

function bundleToItem(bundle: PromotionStrategyDetails): StrategyItem {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromCTPs(ps.spec, bundle.changeTransferPolicies ?? []);
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

const currentParams = (): URLSearchParams => new URLSearchParams(window.location.search);

const commitParams = (params: URLSearchParams) => {
  const url = new URL(window.location.href);
  url.search = params.toString();
  window.history.replaceState(null, '', url.toString());
};

const getParam = (): string => currentParams().get(PARAM) || '';

const setParam = (name: string) => {
  const params = currentParams();
  if (name) {
    params.set(PARAM, name);
  } else {
    params.delete(PARAM);
  }
  commitParams(params);
};

const VIEW_PARAM = 'psView';

const getViewFromUrl = (): ViewMode => {
  const params = currentParams();
  return params.get(VIEW_PARAM) === 'history' || readSelection(params, EXTENSION_SELECTION_PARAMS)
    ? 'history'
    : 'card';
};

const setViewInUrl = (view: ViewMode) => {
  const params = currentParams();
  if (view === 'history') {
    params.set(VIEW_PARAM, view);
    commitParams(params);
  } else {
    params.delete(VIEW_PARAM);
    commitParams(writeSelection(params, EXTENSION_SELECTION_PARAMS, null));
  }
};

const getSelectionFromUrl = (): CellSelection | null =>
  readSelection(currentParams(), EXTENSION_SELECTION_PARAMS);

const setSelectionInUrl = (selection: CellSelection | null) => {
  commitParams(writeSelection(currentParams(), EXTENSION_SELECTION_PARAMS, selection));
};

const getViewStateFromUrl = (): HistoryViewState =>
  readHistoryViewState(currentParams(), EXTENSION_HISTORY_PARAMS);

const setViewStateInUrl = (state: HistoryViewState) => {
  commitParams(writeHistoryViewState(currentParams(), EXTENSION_HISTORY_PARAMS, state));
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
  const [view, setView] = useState<ViewMode>(getViewFromUrl);

  const selectView = (next: ViewMode) => {
    setView(next);
    setViewInUrl(next);
  };

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
      <div className="gp-controls">
        {strategies.length > 1 && (
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
                setStored(application.metadata.namespace, application.metadata.name, key);
              }}
            />
          </div>
        )}
        {selected && (
          <div className="gp-view-toggle" role="tablist" aria-label="View">
            <button
              type="button"
              role="tab"
              aria-selected={view === 'card'}
              className={`gp-view-toggle__btn ${view === 'card' ? 'gp-view-toggle__btn--active' : ''}`}
              onClick={() => selectView('card')}
            >
              Overview
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={view === 'history'}
              className={`gp-view-toggle__btn ${view === 'history' ? 'gp-view-toggle__btn--active' : ''}`}
              onClick={() => selectView('history')}
            >
              History
            </button>
          </div>
        )}
      </div>
      {selected && view === 'card' && (
        <Card environments={selected.promotionStrategy.status?.environments || []} />
      )}
      {selected && view === 'history' && (
        <div className="gp-history-wrapper">
          <HistoryView
            strategy={selected.promotionStrategy}
            initialSelection={getSelectionFromUrl()}
            onSelectionChange={setSelectionInUrl}
            initialViewState={getViewStateFromUrl()}
            onViewStateChange={setViewStateInUrl}
          />
        </div>
      )}
    </div>
  );
};

export default AppViewExtension;
