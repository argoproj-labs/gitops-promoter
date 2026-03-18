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

const AppViewExtension = ({ application }: AppViewComponentProps) => {
  const [strategies, setStrategies] = useState<PromotionStrategy[]>([]);
  const [selectedName, setSelectedName] = useState<string>(getParam);
  const [fetchError, setFetchError] = useState<string | null>(null);

  useEffect(() => {
    const appName = application.metadata.name;
    const appNamespace = application.metadata.namespace;
    const url = `/api/v1/applications/${appName}/managed-resources?appNamespace=${appNamespace}&kind=${KIND}&group=${GROUP}`;

    setFetchError(null);
    fetch(url)
      .then((response) => response.json())
      .then((data) => {
        if (!data.items || data.items.length === 0) {
          throw new Error('No PromotionStrategy resources found');
        }
        const parsed: PromotionStrategy[] = data.items.map((item: { liveState: string }) =>
          JSON.parse(item.liveState),
        );
        setStrategies(parsed);
        const fromUrl = getParam();
        const match = parsed.find((s) => s.metadata.name === fromUrl);
        const initial = match ? fromUrl : parsed[0].metadata.name;
        setSelectedName(initial);
        setParam(initial);
      })
      .catch((err) => {
        setFetchError('Failed to load PromotionStrategy: ' + err);
      });
  }, [application.metadata.name, application.metadata.namespace]);

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
