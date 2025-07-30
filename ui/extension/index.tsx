import React from 'react';
import { enrichPromotionStrategy } from '../shared/src/utils/PSData';
import Card from '../components-lib/src/components/Card';
import './index.scss';

// CRD Objects
interface ResourceMetadata {
  name: string;
  namespace: string;
  uid: string;
  resourceVersion: string;
  generation: number;
  creationTimestamp: string;
  annotations?: Record<string, string>;
  labels?: Record<string, string>;
  ownerReferences?: Array<Record<string, unknown>>;
}

interface CustomResource {
  apiVersion: string;
  kind: string;
  metadata: ResourceMetadata;
  spec: {
    gitRepositoryRef: {
      name: string;
      namespace?: string;
    };
    activeCommitStatuses?: { key: string }[] | null;
    proposedCommitStatuses?: { key: string }[] | null;
    environments: {
      branch: string;
      autoMerge?: boolean;
      activeCommitStatuses?: { key: string }[] | null;
      proposedCommitStatuses?: { key: string }[] | null;
    }[];
  };
  status: Record<string, unknown>;
}

// Argo CD extension component
interface ExtensionProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
  };
  resource: CustomResource;
}

// ArgoCD  definition
declare global {
  interface Window {
    extensionsAPI?: {
      registerResourceExtension: (
        component: React.FC<ExtensionProps>,
        group: string,
        kind: string,
        title: string,
        options?: { icon: string }
      ) => void;
    };
  }
}

const Extension: React.FC<ExtensionProps> = ({ resource }) => {
  const [enrichedEnvs, setEnrichedEnvs] = React.useState<ReturnType<typeof enrichPromotionStrategy>>([]);
  const [isLoading, setIsLoading] = React.useState(true);

  React.useEffect(() => {
    //Checks if data is loaded and sets the state for the component
    let isMounted = true;
    async function enrich() {
      if (resource) {
        const enriched = await enrichPromotionStrategy(resource);
        if (isMounted) {
          setEnrichedEnvs(enriched);
          setIsLoading(false);
        }
      }
    }
    enrich();

    return () => {
      isMounted = false;
    };
  }, [resource]);

  return (
    <div className="extension-container">
      {isLoading ? (
        <div className="loading-indicator">Loading environment details...</div>
      ) : (
        <Card environments={enrichedEnvs} />
      )}
    </div>
  );
};

// Register extension
window.extensionsAPI?.registerResourceExtension(
  Extension,
  'promoter.argoproj.io',
  'PromotionStrategy',
  'PromotionStrategy',
  { icon: 'fa-code-branch' }
);

