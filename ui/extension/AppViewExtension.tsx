import React, { useEffect, useState } from 'react';
import Select, { SingleValue } from 'react-select';
import Card from '@components-lib/components/Card';
import { bundleKey } from '@shared/utils/bundle';
import { fetchPromotionStrategyDetails } from '@shared/utils/fetchPromotionStrategyDetails';
import type { PromotionStrategyBundle } from '@shared/types/bundle';
import { AppViewComponentProps } from '@shared/types/extension';
import './StrategyDropdown.scss';

const GROUP = 'promoter.argoproj.io';
const KIND = 'PromotionStrategy';
const PARAM = 'promotionstrategy';
const STORAGE_PREFIX = 'gitops-promoter:lastStrategy:';

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

const AppViewExtension = ({ application, tree }: AppViewComponentProps) => {
  const [bundles, setBundles] = useState<PromotionStrategyBundle[]>([]);
  const [selectedKey, setSelectedKey] = useState<string>(
    () => getParam() || getStored(application.metadata.namespace, application.metadata.name),
  );
  const [fetchError, setFetchError] = useState<string | null>(null);

  useEffect(() => {
    const appName = application.metadata.name;
    const appNamespace = application.metadata.namespace;
    let ignore = false;

    const strategyNodes = (tree.nodes || []).filter(
      (node) => node.group === GROUP && node.kind === KIND,
    );

    if (strategyNodes.length === 0) {
      setFetchError('No PromotionStrategy resources found');
      setBundles([]);
      setSelectedKey('');
      setParam('');
      setStored(appNamespace, appName, '');
      return;
    }

    setFetchError(null);
    Promise.all(
      strategyNodes.map((node) =>
        fetchPromotionStrategyDetails(appName, appNamespace, node.namespace, node.name),
      ),
    )
      .then((parsed) => {
        if (ignore) return;
        setBundles(parsed);
        const keys = parsed.map(bundleKey);
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
        if (ignore) return;
        const errorMessage = err instanceof Error ? err.message : String(err);
        setFetchError('Failed to load PromotionStrategyDetails: ' + errorMessage);
        setBundles([]);
        setSelectedKey('');
        setParam('');
        setStored(appNamespace, appName, '');
      });

    return () => {
      ignore = true;
    };
  }, [application.metadata.name, application.metadata.namespace, tree]);

  if (bundles.length === 0) {
    if (fetchError) {
      return <div>{fetchError}</div>;
    }
    return <div>Loading...</div>;
  }

  const selected = bundles.find((b) => bundleKey(b) === selectedKey);

  const hasDuplicateNames = new Set(bundles.map((b) => b.metadata.name)).size < bundles.length;

  const options: SelectOption[] = bundles.map((b) => ({
    value: bundleKey(b),
    label: hasDuplicateNames ? `${b.metadata.name} (${b.metadata.namespace})` : b.metadata.name,
  }));

  return (
    <div className="extension-container">
      {bundles.length > 1 && (
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
      {selected && <Card bundle={selected} />}
    </div>
  );
};

export default AppViewExtension;
