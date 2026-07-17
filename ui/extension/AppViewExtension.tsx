import React, { useEffect, useState } from 'react';
import Select, { SingleValue } from 'react-select';
import Card from '@components-lib/components/Card';
import { PromotionStrategy } from '@shared/types/promotion';
import { AppViewComponentProps } from '@shared/types/extension';
import './StrategyDropdown.scss';

const GROUP = 'promoter.argoproj.io';
const KIND = 'PromotionStrategy';
const PARAM = 'promotionstrategy';
const ENV_PARAM = 'env';
const STORAGE_PREFIX = 'gitops-promoter:lastStrategy:';

interface SelectOption {
  value: string;
  label: string;
}

const getParam = (): string => {
  const params = new URLSearchParams(window.location.search);
  return params.get(PARAM) || '';
};

// Deep-link to a single environment card via `&env=<branch>`.
// URLSearchParams handles decoding on read / encoding on write, matching PARAM.
const getEnvParam = (): string => {
  const params = new URLSearchParams(window.location.search);
  return params.get(ENV_PARAM) || '';
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

const setEnvParam = (branch: string) => {
  const url = new URL(window.location.href);
  if (branch) {
    url.searchParams.set(ENV_PARAM, branch);
  } else {
    url.searchParams.delete(ENV_PARAM);
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
  const [strategies, setStrategies] = useState<PromotionStrategy[]>([]);
  const [selectedKey, setSelectedKey] = useState<string>(
    () => getParam() || getStored(application.metadata.namespace, application.metadata.name),
  );
  const [fetchError, setFetchError] = useState<string | null>(null);
  // The env deep-link highlights a specific env card. `highlightBranch` mirrors
  // the `env` URL param: seeded from the URL at load time and re-synced whenever
  // the URL changes in place (e.g. a "View Details" link that mutates the param
  // via the History API).
  const [highlightBranch, setHighlightBranch] = useState<string>(() => getEnvParam());

  const focusEnv = (branch: string) => {
    setEnvParam(branch);
    setHighlightBranch(branch);
  };

  useEffect(() => {
    const onPopState = () => {
      const urlKey = getParam();
      if (urlKey) {
        setSelectedKey(urlKey);
      }
      setHighlightBranch(getEnvParam());
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

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
        return JSON.parse(data.manifest) as PromotionStrategy;
      }),
    )
      .then((parsed) => {
        setStrategies(parsed);
        const keys = parsed.map(strategyKey);
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

  const selected = strategies.find((s) => strategyKey(s) === selectedKey);

  const hasDuplicateNames =
    new Set(strategies.map((s) => s.metadata.name)).size < strategies.length;

  const options: SelectOption[] = strategies.map((s) => ({
    value: strategyKey(s),
    label: hasDuplicateNames ? `${s.metadata.name} (${s.metadata.namespace})` : s.metadata.name,
  }));

  return (
    <div className="extension-container">
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
              // Env focus belongs to a specific strategy; clear it on switch.
              focusEnv('');
              setStored(application.metadata.namespace, application.metadata.name, key);
            }}
          />
        </div>
      )}
      {selected && (
        <Card
          key={selectedKey}
          environments={selected.status?.environments || []}
          highlightBranch={highlightBranch}
          onFocusChange={(branch) => focusEnv(branch ?? '')}
        />
      )}
    </div>
  );
};

export default AppViewExtension;
