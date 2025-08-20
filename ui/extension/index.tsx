import React from 'react';
import './index.scss';
import StatusPanelComponent from './statusPanel';
import ResourceExtension from './resourceExtension';
import type { PromotionStrategy } from '../shared/src/types/promotion';

// ArgoCD extension API definitions
interface ResourceExtensionProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
  };
  resource: PromotionStrategy;
}

interface StatusPanelProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
    status?: {
      resources?: Array<{
        kind: string;
        group: string;
        name: string;
        namespace: string;
        status: string;
      }>;
    };
  };
}

declare global {
  interface Window {
    extensionsAPI?: {
      registerResourceExtension: (
        component: React.FC<ResourceExtensionProps>,
        group: string,
        kind: string,
        title: string,
        options?: { icon: string }
      ) => void;

      registerStatusPanelExtension: (
        component: React.FC<StatusPanelProps>,
        title: string,
        id: string,
        flyout?: React.FC<StatusPanelProps>
      ) => void;
    };
  }
}

// Register resource extension (PromotionStrategy tab)
window.extensionsAPI?.registerResourceExtension(
  ResourceExtension,
  'promoter.argoproj.io',
  'PromotionStrategy',
  'PromotionStrategy',
  { icon: 'fa-code-branch' }
);

// Register status panel extension 
window.extensionsAPI?.registerStatusPanelExtension(
  StatusPanelComponent,
  'Promotion Strategy',
  'promotion_strategy_status'
);
