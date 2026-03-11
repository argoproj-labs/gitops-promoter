import React, { useEffect, useMemo, useState } from 'react';
import Card from '@components-lib/components/Card';
import { PromotionStrategy } from '@shared/types/promotion';
import { AppViewComponentProps } from '@shared/types/extension';

const GROUP = 'promoter.argoproj.io';
const KIND = 'PromotionStrategy';

const AppViewExtension = ({ tree, application }: AppViewComponentProps) => {
  const promotionStrategyNodes = useMemo(
    () => tree.nodes.filter((node) => node.kind === KIND && node.group === GROUP),
    [tree.nodes],
  );
  const [promotionStrategy, setPromotionStrategy] = useState<PromotionStrategy>();
  const [fetchError, setFetchError] = useState<string | null>(null);

  useEffect(() => {
    const appName = application.metadata.name;
    const appNamespace = application.metadata.namespace;
    let url: string;

    if (promotionStrategyNodes.length >= 1) {
      const node = promotionStrategyNodes[0];
      const version = node.version ?? 'v1alpha1';
      url = `/api/v1/applications/${appName}/resource?appNamespace=${appNamespace}&name=${node.name}&namespace=${node.namespace}&resourceName=${node.name}&version=${version}&kind=${KIND}&group=${GROUP}`;
    } else {
      url = `/api/v1/applications/${appName}/resource?appNamespace=${appNamespace}&kind=${KIND}&group=${GROUP}`;
    }

    setFetchError(null);
    fetch(url)
      .then((response) => response.json())
      .then((data) => {
        setPromotionStrategy(
          typeof data.manifest === 'string' ? JSON.parse(data.manifest) : data.manifest,
        );
      })
      .catch((err) => {
        console.error('Error fetching promotion strategy data: ' + err);
        setFetchError('Failed to load PromotionStrategy: ' + err);
      });
  }, [promotionStrategyNodes, application.metadata.name, application.metadata.namespace]);

  if (!promotionStrategy) {
    if (fetchError) {
      return <div>{fetchError}</div>;
    }
    return <div>Loading...</div>;
  }
  return (
    <div className="extension-container">
      <Card environments={promotionStrategy.status?.environments || []} />
    </div>
  );
};

export default AppViewExtension;
