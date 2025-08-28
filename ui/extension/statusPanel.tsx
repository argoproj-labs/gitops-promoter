import React from 'react';
import axios from 'axios';
import { getPromotionStatus } from '../shared/src/utils/getStatus';
import { StatusIcon } from '../components-lib/src/components/StatusIcon';
import type { StatusType } from '../components-lib/src/components/StatusIcon';
import type { PromotionPhase, PromotionStrategy } from '../shared/src/types/promotion';

// Types
interface Application {
  metadata: { name: string; namespace: string };
  status?: {
    resources?: Array<{
      kind: string;
      group: string;
      name: string;
      namespace: string;
      status: string;
    }>;
  };
}

interface Resource {
  kind: string;
  name: string;
  namespace: string;
  liveState?: string;
}

const findPromotionStrategy = (application: Application) => {
  const promotionStrategies = application.status?.resources?.filter(resource => 
    resource.kind === 'PromotionStrategy' && 
    resource.group === 'promoter.argoproj.io'
  );
  
  if (!promotionStrategies || promotionStrategies.length === 0) {
    return null;
  }
  
  // Multiple promotions found
  if (promotionStrategies.length > 1) {
    return { error: 'Multiple promotions found' };
  }
  
  return promotionStrategies[0];
};

// Only way to get CRD data is to fetch from ArgoCD API
const fetchPromotionStrategyData = async (application: Application, promotionStrategy: { name: string; namespace: string }): Promise<PromotionStrategy> => {
  const response = await axios.get(`/api/v1/applications/${application.metadata.name}/managed-resources?_t=${Date.now()}`, {
    headers: {
      'Cache-Control': 'no-cache, no-store, must-revalidate',
      'Pragma': 'no-cache',
      'Expires': '0'
    }
  });
  
  const resource = response.data.items?.find((r: Resource) => 
    r.kind === 'PromotionStrategy' &&
    r.name === promotionStrategy.name &&
    r.namespace === promotionStrategy.namespace
  );
  
  if (!resource?.liveState) {
    throw new Error('PromotionStrategy not found');
  }
  
  return JSON.parse(resource.liveState);
};

const getStatusInfo = (phase: PromotionPhase) => {
  const statusText = {
    promoted: 'Promoted',
    failure: 'Failed', 
    pending: 'Pending',
    unknown: 'Unknown'
  }[phase] || 'Unknown';
  
  const statusIcon = phase === 'promoted' ? 'success' : phase as StatusType;
  
  return { text: statusText, icon: statusIcon };
};

// Status panel component for promotion strategy summary
const StatusPanelComponent: React.FC<{ application: Application }> = ({ application }) => {
  const [promotionData, setPromotionData] = React.useState({
    phase: 'unknown' as PromotionPhase,
    total: 0,
    promoted: 0,
    summary: '',
    loading: false,
    error: null as string | null
  });

  // Poll from ArgoCD API
  React.useEffect(() => {
    const promotionStrategy = findPromotionStrategy(application);
    if (!promotionStrategy) return;

    // Handle multiple promotion strategies error
    if ('error' in promotionStrategy) {
      setPromotionData(prev => ({ ...prev, error: promotionStrategy.error }));
      return;
    }

    const loadPromotionData = async () => {
      try {
        const crdData = await fetchPromotionStrategyData(application, promotionStrategy);
        const { overallStatus, total, promoted, displayText } = getPromotionStatus(crdData);
        
        setPromotionData({
          phase: overallStatus,
          total,
          promoted,
          summary: displayText,
          loading: false,
          error: null
        });
      } catch (error) {
        setPromotionData(prev => ({ 
          ...prev, 
          loading: false, 
          error: 'Failed to load promotion data' 
        }));
      }
    };

    setPromotionData(prev => ({ ...prev, loading: true, error: null }));
    loadPromotionData();

    const interval = setInterval(loadPromotionData, 4000);
    return () => clearInterval(interval);
  }, [application]);

  // Don't render if no promotion strategy found
  const promotionStrategy = findPromotionStrategy(application);
  if (!promotionStrategy) {
    return null;
  }

  // Display error if it exists
  if ('error' in promotionStrategy) {
    return (
      <div className="application-status-panel__item">
        <label className="promotion-status-label">Promotion Status</label>
        <div className="application-status-panel__item-value">
          <span>{promotionStrategy.error}</span>
        </div>
      </div>
    );
  }

  const { phase, total, summary, loading, error } = promotionData;
  const { text: statusText, icon: statusIcon } = getStatusInfo(phase);

  const navigateToPromotionStrategyTab = () => {
    const treeUrl = `/applications/${application.metadata.name}?view=tree&node=promoter.argoproj.io%2FPromotionStrategy%2F${promotionStrategy.namespace}%2F${promotionStrategy.name}&tab=extension-0`;
    window.history.pushState({}, '', treeUrl);
    window.dispatchEvent(new PopStateEvent('popstate'));
  };



  // 2. Status display - use getPromotionPhase from PSData

  return (
    <div className="application-status-panel__item">
      <label className="promotion-status-label">Promotion Status</label>
      
      <div className={`application-status-panel__item-value application-status-panel__item-value--${statusText}`}>
        <StatusIcon phase={statusIcon} type="status" />
        <span className={`promotion-status-text ${statusText.toLowerCase()}`}>{statusText}</span>
      </div>
      
      {loading && <div>Loading promotion data...</div>}
      {error && <div>{error}</div>}
      
      {total > 0 && (
        <div className="promotion-summary">
          {summary}
        </div>
      )}
      
      <button onClick={navigateToPromotionStrategyTab} className="argo-button argo-button--base">
        View Details
      </button>
    </div>
  );
};

export default StatusPanelComponent;