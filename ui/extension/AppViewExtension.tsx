import React, { useEffect, useState } from 'react';
import Select, { SingleValue } from 'react-select';
import Card from '@components-lib/components/Card';
import { PromotionStrategy } from '@shared/types/promotion';
import { AppViewComponentProps } from '@shared/types/extension';
import './StrategyDropdown.scss';

const GROUP = 'promoter.argoproj.io';
const KIND = 'PromotionStrategy';
const PARAM = 'promotionstrategy';

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

const AppViewExtension = ({ application, tree }: AppViewComponentProps) => {
  const [strategies, setStrategies] = useState<PromotionStrategy[]>([]);
  const [selectedName, setSelectedName] = useState<string>(getParam);
  const [fetchError, setFetchError] = useState<string | null>(null);

  useEffect(() => {
    const appName = application.metadata.name;
    const appNamespace = application.metadata.namespace;

    const strategyNodes = (tree.nodes || []).filter(
      (node) => node.group === GROUP && node.kind === KIND,
    );

    if (strategyNodes.length === 0) {
      setFetchError('No PromotionStrategy resources found');
      setStrategies([]);
      setSelectedName('');
      setParam('');
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
        const fromUrl = getParam();
        const match = parsed.find((s) => s.metadata.name === fromUrl);
        const initial = match ? fromUrl : parsed[0].metadata.name;
        setSelectedName(initial);
        setParam(initial);
      })
      .catch((err) => {
        const errorMessage = err instanceof Error ? err.message : String(err);
        setFetchError('Failed to load PromotionStrategy: ' + errorMessage);
        setStrategies([]);
        setSelectedName('');
        setParam('');
      });
  }, [application.metadata.name, application.metadata.namespace, tree]);

  if (strategies.length === 0) {
    if (fetchError) {
      return <div>{fetchError}</div>;
    }
    return <div>Loading...</div>;
  }

  const selected = strategies.find((s) => s.metadata.name === selectedName);

  const options: SelectOption[] = strategies.map((s) => ({
    value: s.metadata.name,
    label: s.metadata.name,
  }));

  return (
    <div className="extension-container">
      {strategies.length > 1 && (
        <div className="strategy-dropdown-wrapper">
          <Select<SelectOption>
            classNamePrefix="strategy-dropdown"
            options={options}
            placeholder="Select a PromotionStrategy"
            value={options.find((opt) => opt.value === selectedName) || null}
            onChange={(option: SingleValue<SelectOption>) => {
              const name = option ? option.value : '';
              setSelectedName(name);
              setParam(name);
            }}
          />
        </div>
      )}
      {selected && <Card environments={selected.status?.environments || []} />}
    </div>
  );
};

export default AppViewExtension;
