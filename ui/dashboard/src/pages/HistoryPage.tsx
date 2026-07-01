import React, { useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import HistoryView from '@lib/components/HistoryView/HistoryView';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';

/**
 * Dashboard route wrapper around the shared HistoryView. Resolves the
 * PromotionStrategy from the store/route params and wires up router-based
 * back navigation; all rendering lives in the shared component so the ArgoCD
 * extension can reuse it without a router.
 */
const HistoryPage: React.FC = () => {
  const { namespace, name } = useParams();
  const navigate = useNavigate();
  const { items, fetchItems } = PromotionStrategyStore();

  useEffect(() => {
    if (namespace) fetchItems(namespace);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace]);

  const strategy = items.find((ps) => ps.metadata?.name === name);

  return (
    <HistoryView
      strategy={strategy}
      name={name}
      namespace={namespace}
      onBack={() => navigate(`/promotion-strategies/${namespace}/${name}`)}
    />
  );
};

export default HistoryPage;
