import React, { useEffect, useState } from 'react';
import Card from '@components-lib/components/Card';
import { fetchPromotionStrategyDetails } from '@shared/utils/fetchPromotionStrategyDetails';
import type { PromotionStrategyBundle } from '@shared/types/bundle';
import { ResourceExtensionProps } from '@shared/types/extension';

// Argo CD extension component

const ResourceExtension: React.FC<ResourceExtensionProps> = ({ application, resource }) => {
  const [bundle, setBundle] = useState<PromotionStrategyBundle | null>(null);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const psNamespace = resource.metadata?.namespace;
    const psName = resource.metadata?.name;
    let ignore = false;

    if (!psNamespace || !psName) {
      setFetchError('PromotionStrategy metadata is missing name or namespace');
      setBundle(null);
      setLoading(false);
      return;
    }

    setFetchError(null);
    setLoading(true);
    fetchPromotionStrategyDetails(
      application.metadata.name,
      application.metadata.namespace,
      psNamespace,
      psName,
    )
      .then((fetched) => {
        if (ignore) return;
        setBundle(fetched);
      })
      .catch((err) => {
        if (ignore) return;
        const errorMessage = err instanceof Error ? err.message : String(err);
        setFetchError('Failed to load PromotionStrategyDetails: ' + errorMessage);
        setBundle(null);
      })
      .finally(() => {
        if (!ignore) setLoading(false);
      });

    return () => {
      ignore = true;
    };
  }, [
    application.metadata.name,
    application.metadata.namespace,
    resource.metadata?.name,
    resource.metadata?.namespace,
  ]);

  if (fetchError) {
    return <div className="extension-container">{fetchError}</div>;
  }

  if (loading) {
    return <div className="extension-container">Loading...</div>;
  }

  return <div className="extension-container">{bundle && <Card bundle={bundle} />}</div>;
};

export default ResourceExtension;
