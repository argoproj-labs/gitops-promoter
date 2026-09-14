import React, { useEffect, useCallback } from 'react';
import { useParams, useSearchParams } from 'react-router';
import HistoryView from '@lib/components/HistoryView/HistoryView';
import type { CellSelection } from '@lib/components/HistoryView/HistoryView';
import {
  DASHBOARD_HISTORY_PARAMS,
  DASHBOARD_SELECTION_PARAMS,
  readHistoryViewState,
  readSelection,
  writeHistoryViewState,
  writeSelection,
} from '@shared/utils/deepLink';
import type { HistoryViewState } from '@shared/utils/deepLink';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';
import { useNavigateWithParams } from '../hooks/useNavigateWithParams';

const HistoryPage: React.FC = () => {
  const { namespace, name } = useParams();
  const navigate = useNavigateWithParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const { items, fetchItems } = PromotionStrategyStore();

  useEffect(() => {
    if (namespace) fetchItems(namespace);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace]);

  const strategy = items.find((ps) => ps.metadata.name === name);

  const initialSelection = readSelection(searchParams, DASHBOARD_SELECTION_PARAMS);
  const initialViewState = readHistoryViewState(searchParams, DASHBOARD_HISTORY_PARAMS);

  const handleSelectionChange = useCallback(
    (selection: CellSelection | null) => {
      setSearchParams((prev) => writeSelection(prev, DASHBOARD_SELECTION_PARAMS, selection), {
        replace: true,
      });
    },
    [setSearchParams],
  );

  const handleViewStateChange = useCallback(
    (state: HistoryViewState) => {
      setSearchParams((prev) => writeHistoryViewState(prev, DASHBOARD_HISTORY_PARAMS, state), {
        replace: true,
      });
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
      initialViewState={initialViewState}
      onViewStateChange={handleViewStateChange}
    />
  );
};

export default HistoryPage;
