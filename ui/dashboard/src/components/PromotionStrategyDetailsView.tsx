import React from 'react';
import Card from '@lib/components/Card';
import { type PromotionStrategy } from '@shared/utils/PSData';
import type { GitRepository, ScmProvider, ClusterScmProvider } from '@shared/types/view';

interface PromotionStrategyDetailsViewProps {
  strategy: PromotionStrategy & {
    gitRepository?: GitRepository;
    scmProvider?: ScmProvider;
    clusterScmProvider?: ClusterScmProvider;
  };
}

export const PromotionStrategyDetailsView: React.FC<PromotionStrategyDetailsViewProps> = ({
  strategy,
}) => {
  const environments = strategy.status?.environments || [];

  return (
    <Card
      environments={environments}
      promotionStrategy={strategy}
      gitRepository={strategy.gitRepository}
      scmProvider={strategy.scmProvider}
      clusterScmProvider={strategy.clusterScmProvider}
    />
  );
};

export default PromotionStrategyDetailsView;
