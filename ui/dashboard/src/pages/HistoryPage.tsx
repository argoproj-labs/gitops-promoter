import React, { useEffect, useCallback } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import HistoryView from '@lib/components/HistoryView/HistoryView';
import type { CellSelection } from '@lib/components/HistoryView/HistoryView';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';

const HistoryPage: React.FC = () => {
  const { namespace, name } = useParams();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const { items, fetchItems } = PromotionStrategyStore();

  useEffect(() => {
    if (namespace) fetchItems(namespace);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace]);

  const strategy = items.find((ps) => ps.metadata.name === name);

  const commit = searchParams.get('commit');
  const env = searchParams.get('env');
  const initialSelection = commit && env ? { rowId: commit, branch: env } : null;

  const handleSelectionChange = useCallback(
    (selection: CellSelection | null) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (selection) {
            next.set('commit', selection.rowId);
            next.set('env', selection.branch);
          } else {
            next.delete('commit');
            next.delete('env');
          }
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  return (
    <HistoryView
      strategy={strategy}
      name={name}
      namespace={namespace}
      onBack={() => navigate(`/promotion-strategies/${namespace}/${name}`)}
      fillViewport
      initialSelection={initialSelection}
      onSelectionChange={handleSelectionChange}
    />
  );
};

export default HistoryPage;
