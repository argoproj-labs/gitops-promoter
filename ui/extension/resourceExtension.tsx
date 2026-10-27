import React from 'react';
import PromotionExplorer from '@components-lib/components/PromotionExplorer/PromotionExplorer';
import { ResourceExtensionProps } from '@shared/types/extension';

const ResourceExtension: React.FC<ResourceExtensionProps> = ({ resource }) => {
  return (
    <div className="extension-container">
      <PromotionExplorer strategy={resource} />
    </div>
  );
};

export default ResourceExtension;
