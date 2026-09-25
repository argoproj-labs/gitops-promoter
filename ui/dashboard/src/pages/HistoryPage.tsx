import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useParams } from 'react-router';
import HistoryView from '@lib/components/HistoryView/HistoryView';
import type { HistoryUrlState } from '@lib/components/HistoryView/HistoryView';
import {
  DASHBOARD_HISTORY_PARAMS,
  DASHBOARD_SELECTION_PARAMS,
  readHistoryViewState,
  readSelection,
  writeHistoryViewState,
  writeSelection,
} from '@shared/utils/deepLink';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';
import { useNavigateWithParams } from '../hooks/useNavigateWithParams';

const currentParams = (): URLSearchParams => new URLSearchParams(window.location.search);

const commitParams = (params: URLSearchParams) => {
  const url = new URL(window.location.href);
  url.search = params.toString();
  window.history.replaceState(window.history.state, '', url.toString());
};

const HistoryPage: React.FC = () => {
  const { namespace, name } = useParams();
  const navigate = useNavigateWithParams();
  const { items, fetchItems } = PromotionStrategyStore();

  const [initialUrlState] = useState(() => ({
    selection: readSelection(currentParams(), DASHBOARD_SELECTION_PARAMS),
    viewState: readHistoryViewState(currentParams(), DASHBOARD_HISTORY_PARAMS),
  }));

  const fetchedNamespaceRef = useRef<string | null>(null);
  useEffect(() => {
    if (namespace && fetchedNamespaceRef.current !== namespace) {
      fetchedNamespaceRef.current = namespace;
      fetchItems(namespace);
    }
  }, [namespace, fetchItems]);

  const strategy = items.find((ps) => ps.metadata.name === name);

  const handleUrlStateChange = useCallback((state: HistoryUrlState) => {
    let params = writeSelection(currentParams(), DASHBOARD_SELECTION_PARAMS, state.selection);
    params = writeHistoryViewState(params, DASHBOARD_HISTORY_PARAMS, state.viewState);
    commitParams(params);
  }, []);

  return (
    <HistoryView
      strategy={strategy}
      name={name}
      namespace={namespace}
      onBack={() => navigate(`/promotion-strategies/${namespace}/${name}`)}
      fillViewport
      initialSelection={initialUrlState.selection}
      initialViewState={initialUrlState.viewState}
      onUrlStateChange={handleUrlStateChange}
    />
  );
};

export default HistoryPage;
