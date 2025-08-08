import React, { useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { namespaceStore } from '../stores/NamespaceStore'
import { viewStore } from '../stores/ViewStore';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';
import BackButton from '../components/BackButton';
import HeaderBar from '@lib/components/HeaderBar';
import PromotionStrategyDetailsView from '@lib/components/PromotionStrategyDetailsView';
import { LiveManifestView } from '../components/LiveManifestView';
import type { PromotionStrategy } from '@shared/utils/PSData';
import './PromotionStrategyPage.scss';

interface NamespaceStore {
  namespace: string;
  namespaces: string[];
  setNamespace: (namespace: string) => void;
  setNamespaces: (namespaces: string[]) => void;
}

interface PromotionStrategyPageProps {
  namespace?: string;
  strategyName?: string;
}

const PromotionStrategyPage: React.FC<PromotionStrategyPageProps> = ({ namespace: propsNamespace, strategyName: propsStrategyName }) => {
  const { namespace: urlNamespace, name: urlStrategyName } = useParams();
  const namespace = propsNamespace || urlNamespace;
  const strategyName = propsStrategyName || urlStrategyName;

  const currentNamespace = namespaceStore((s: NamespaceStore) => s.namespace);
  const setNamespace = namespaceStore((s: NamespaceStore) => s.setNamespace);
  const { currentView, setView } = viewStore();

  const { items, fetchItems, subscribe, unsubscribe } = PromotionStrategyStore();

  // Find the selected strategy
  const selectedStrategy = items.find(
    (ps: PromotionStrategy) => ps.metadata?.name === strategyName
  );

  useEffect(() => {
    if (!namespace) return;
    if (namespace !== currentNamespace) {
      setNamespace(namespace);
    }

    if (!items.length || !selectedStrategy) {
      fetchItems(namespace);
    }
    
    subscribe(namespace);
    return () => unsubscribe();
  }, [namespace, currentNamespace, setNamespace, fetchItems, subscribe, unsubscribe, items, selectedStrategy]);

  const navigate = useNavigate();

  const handleBack = () => {
    setNamespace(currentNamespace);
    navigate('/promotion-strategies');
  };


  // Loading State
  if (items.length === 0) {
    return <div style={{ textAlign: 'center', marginTop: '20px' }}>Loading strategies...</div>;
  }

  // Not found state
  if (!selectedStrategy) {
    return <div style={{ textAlign: 'center', marginTop: '20px' }}>No strategy found for {strategyName}</div>;
  }

  return (
    <>
      <div className="strategy-page-header">


        <div className="strategy-page-header-left">
          <BackButton onClick={handleBack} />
        </div>


        <div className="strategy-page-header-center">
          <HeaderBar name={strategyName || ""} />
        </div>

        
        <div className="strategy-page-header-right">
          <div className="strategy-page-tabs">
            <button
              className={`strategy-page-tab ${currentView === 'cards' ? 'active' : ''}`}
              onClick={() => setView('cards')}
            >
              Overview
            </button>

            
            <button
              className={`strategy-page-tab ${currentView === 'json' ? 'active' : ''}`}
              onClick={() => setView('json')}
            >
              Live Manifest
            </button>
          </div>
        </div>
      </div>

      {currentView === 'cards' ? (
        <div style={{ marginTop: '40px' }}>
          <PromotionStrategyDetailsView strategy={selectedStrategy} />
        </div>
      ) : (
        <LiveManifestView strategy={selectedStrategy} />
      )}
    </>
  );
};

export default PromotionStrategyPage; 