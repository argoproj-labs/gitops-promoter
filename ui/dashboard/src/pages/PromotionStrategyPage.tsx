import React, { useEffect } from 'react';
import { useLocation, useParams } from 'react-router';
import { useNavigateWithParams } from '../hooks/useNavigateWithParams';
import { namespaceStore } from '../stores/NamespaceStore';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';
import BackButton from '../components/BackButton';
import HeaderBar from '@lib/components/HeaderBar';
import PromotionStrategyDetailsView from '../components/PromotionStrategyDetailsView';
import { LiveManifestView } from '@lib/components/LiveManifestView';
import type { PromotionStrategy } from '@shared/utils/PSData';
import './PromotionStrategyPage.scss';

interface NamespaceStore {
  namespace: string;
  setNamespace: (_ns: string) => void;
}

interface PromotionStrategyPageProps {
  namespace?: string;
  strategyName?: string;
}

const PromotionStrategyPage: React.FC<PromotionStrategyPageProps> = ({
  namespace: propsNamespace,
  strategyName: propsStrategyName,
}) => {
  const { namespace: urlNamespace, name: urlStrategyName } = useParams();
  const namespace = propsNamespace || urlNamespace;
  const strategyName = propsStrategyName || urlStrategyName;

  const currentNamespace = namespaceStore((s: NamespaceStore) => s.namespace);
  const setNamespace = namespaceStore((s: NamespaceStore) => s.setNamespace);
  const { pathname } = useLocation();
  const showManifest = pathname.endsWith('/manifest');

  const { items, fetchItems, subscribe, unsubscribe } = PromotionStrategyStore();

  const selectedStrategy = items.find((ps: PromotionStrategy) => ps.metadata.name === strategyName);

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
  }, [
    namespace,
    currentNamespace,
    setNamespace,
    fetchItems,
    subscribe,
    unsubscribe,
    items,
    selectedStrategy,
  ]);

  const navigate = useNavigateWithParams();
  const strategyPath = `/promotion-strategies/${namespace}/${strategyName}`;

  const handleBack = () => {
    setNamespace(currentNamespace);
    navigate('/promotion-strategies');
  };

  if (items.length === 0) {
    return (
      <div style={{ textAlign: 'center', marginTop: '20px' }}>Loading promotion strategies…</div>
    );
  }

  if (!selectedStrategy) {
    return (
      <div style={{ textAlign: 'center', marginTop: '20px' }}>
        We couldn't find a promotion strategy named {strategyName}.
      </div>
    );
  }

  return (
    <>
      <div className="strategy-page-header">
        <div className="strategy-page-header-left">
          <BackButton onClick={handleBack} />
        </div>

        <div className="strategy-page-header-center">
          <HeaderBar name={strategyName || ''} />
        </div>

        <div className="strategy-page-header-right">
          <div className="strategy-page-tabs">
            <button
              className={`strategy-page-tab ${!showManifest ? 'active' : ''}`}
              onClick={() => navigate(strategyPath)}
            >
              Overview
            </button>

            <button
              className="strategy-page-tab"
              onClick={() => navigate(`${strategyPath}/history`)}
            >
              History
            </button>

            <button
              className={`strategy-page-tab ${showManifest ? 'active' : ''}`}
              onClick={() => navigate(`${strategyPath}/manifest`)}
            >
              Live
              <br />
              manifest
            </button>
          </div>
        </div>
      </div>

      {showManifest ? (
        <LiveManifestView strategy={selectedStrategy} />
      ) : (
        <div className="strategy-page-cards">
          <PromotionStrategyDetailsView strategy={selectedStrategy} />
        </div>
      )}
    </>
  );
};

export default PromotionStrategyPage;
