import React from 'react';
import { enrichPromotionStrategy, PromotionStrategy } from '../shared/src/utils/PSData';
import Card from '../components-lib/src/components/Card';
import './index.scss';

// Argo CD extension component
interface ExtensionProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
  };
  resource: PromotionStrategy;
}

const ResourceExtension: React.FC<ExtensionProps> = ({ resource }) => {
  const enrichedEnvs = enrichPromotionStrategy(resource);

  return (
    <div className="extension-container">
      <Card environments={enrichedEnvs} />
    </div>
  );
};

export default ResourceExtension; 