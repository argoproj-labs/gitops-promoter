import React from 'react';
import './index.scss';
// import StatusPanelComponent from './statusPanel';  // Temporarily disabled due to dependency issues
// import ResourceExtension from './resourceExtension';  // Temporarily disabled due to dependency issues
import LiveManifestView from './liveManifestView';

// Simplified types to avoid dependency issues
interface SimpleResourceExtensionProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
  };
  resource: any;  // Use any for now to avoid type complexity
}

// Simple placeholder component for ResourceExtension
const ResourceExtensionPlaceholder: React.FC<SimpleResourceExtensionProps> = () => {
  return (
    <div style={{ padding: '20px' }}>
      <h3>PromotionStrategy Details</h3>
      <p>This tab is temporarily disabled while implementing the Live Manifest view.</p>
    </div>
  );
};

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
        component: React.FC<SimpleResourceExtensionProps>,
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

// Register resource extension (PromotionStrategy tab) - temporarily using placeholder
window.extensionsAPI?.registerResourceExtension(
  ResourceExtensionPlaceholder,
  'promoter.argoproj.io',
  'PromotionStrategy',
  'PromotionStrategy',
  { icon: 'fa-code-branch' }
);

// Register Live Manifest tab
window.extensionsAPI?.registerResourceExtension(
  LiveManifestView,
  'promoter.argoproj.io',
  'PromotionStrategy',
  'Live Manifest',
  { icon: 'fa-file-code' }
);

// Register status panel extension - temporarily disabled due to dependency issues
// window.extensionsAPI?.registerStatusPanelExtension(
//   StatusPanelComponent,
//   'Promotion Strategy',
//   'promotion_strategy_status'
// );
