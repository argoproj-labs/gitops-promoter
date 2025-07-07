import React, { useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { namespaceStore } from '@shared/stores/NamespaceStore'
import { PromotionStrategyStore } from '@shared/stores/PromotionStrategyStore';
import BackButton from '../components/BackButton';
import HeaderBar from '@lib/components/HeaderBar';
import PromotionStrategyDetailsView from '@lib/components/PromotionStrategyDetailsView';

interface PromotionStrategyPageProps {
  namespace?: string;
  strategyName?: string;
}

const PromotionStrategyPage: React.FC<PromotionStrategyPageProps> = ({ namespace: propsNamespace, strategyName: propsStrategyName }) => {
  const { namespace: urlNamespace, name: urlStrategyName } = useParams();
  const namespace = propsNamespace || urlNamespace;
  const strategyName = propsStrategyName || urlStrategyName;

  const currentNamespace = namespaceStore((s: any) => s.namespace);
  const setNamespace = namespaceStore((s: any) => s.setNamespace);

  const { items, fetchItems, subscribe, unsubscribe } = PromotionStrategyStore();

  // Find the selected strategy
  const selectedStrategy = items.find(
    (ps: any) => ps.metadata.name === strategyName
  );

  useEffect(() => {
    if (!namespace) return;
    if (namespace !== currentNamespace) {
      setNamespace(namespace);
    }
    // Only fetch if items are empty or selectedStrategy is not found
    if (!items || items.length === 0 || !selectedStrategy) {
      fetchItems(namespace);
    }
    subscribe(namespace);
    return () => unsubscribe();
  }, [namespace, currentNamespace, setNamespace, fetchItems, subscribe, unsubscribe, items, selectedStrategy]);

  //Navigation:
  const navigate = useNavigate();

  const handleBack = () => {
    setNamespace(currentNamespace);
    navigate('/promotion-strategies');
  };

  
  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', position: 'relative', width: '100%', backgroundColor: 'white'}}>
        <div style={{ flex: '0 0 auto' }}>
          <BackButton onClick={handleBack} />
        </div>
        <div style={{ flex: 1, display: 'flex', justifyContent: 'center', marginRight: '100px'}}>
          <HeaderBar name={strategyName || ""} />
        </div>
      </div>

      {items.length === 0 ? (
        <div style={{ textAlign: 'center', marginTop: '20px' }}>Loading strategies...</div>
      ) : selectedStrategy ? (
        <div style = {{marginTop: '40px'}}>
        <PromotionStrategyDetailsView
          strategy={selectedStrategy}
        />
        </div>
      ) : (
        <div style={{ textAlign: 'center', marginTop: '20px' }}>No strategy found for {strategyName}</div>
      )}
    </>
  );
};

export default PromotionStrategyPage; 