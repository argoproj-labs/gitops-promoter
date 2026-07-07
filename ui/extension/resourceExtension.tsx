import React, { useEffect, useState } from 'react';
import Card from '@components-lib/components/Card';
import { environmentsFromCTPs, repoURLFromBundle } from '@shared/utils/bundle';
import { fetchPromotionStrategyDetails } from '@shared/utils/fetchPromotionStrategyDetails';
import type { Environment } from '@shared/types/promotion';
import { ResourceExtensionProps } from '@shared/types/extension';

// Argo CD extension component

const ResourceExtension: React.FC<ResourceExtensionProps> = ({ application, resource }) => {
  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [deploymentRepoURL, setDeploymentRepoURL] = useState('');
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const psNamespace = resource.metadata?.namespace;
    const psName = resource.metadata?.name;
    if (!psNamespace || !psName) {
      setFetchError('PromotionStrategy metadata is missing name or namespace');
      setEnvironments([]);
      setDeploymentRepoURL('');
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
      .then((bundle) => {
        setEnvironments(
          environmentsFromCTPs(bundle.promotionStrategy, bundle.changeTransferPolicies ?? []),
        );
        setDeploymentRepoURL(repoURLFromBundle(bundle));
      })
      .catch((err) => {
        const errorMessage = err instanceof Error ? err.message : String(err);
        setFetchError('Failed to load PromotionStrategyDetails: ' + errorMessage);
        setEnvironments([]);
        setDeploymentRepoURL('');
      })
      .finally(() => setLoading(false));
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

  return (
    <div className="extension-container">
      <Card environments={environments} deploymentRepoURL={deploymentRepoURL} />
    </div>
  );
};

export default ResourceExtension;
