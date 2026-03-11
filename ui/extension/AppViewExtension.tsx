import React, { useEffect, useState } from 'react';
import Card from '@components-lib/components/Card';
import { PromotionStrategy } from '@shared/types/promotion';
import { AppViewComponentProps } from '@shared/types/extension';

const GROUP = 'promoter.argoproj.io';
const KIND = 'PromotionStrategy';

const AppViewExtension = ({ application }: AppViewComponentProps) => {
  const [promotionStrategy, setPromotionStrategy] = useState<PromotionStrategy>();
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
        setPromotionStrategy(JSON.parse(data.items[0].liveState));
      })
      .catch((err) => {
        console.error('Error fetching promotion strategy data: ' + err);
        setFetchError('Failed to load PromotionStrategy: ' + err);
      });
  }, [application.metadata.name, application.metadata.namespace]);

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
