import React from 'react';
import { PromotionStrategy } from '../shared/src/utils/PSData';
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


  //Pass raw data to Card component
  const environments = resource.status?.environments || [];

  return (
    <div className="extension-container">
      <Card environments={environments} />
    </div>
  );
};

export default ResourceExtension; 