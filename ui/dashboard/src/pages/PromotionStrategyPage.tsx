import React, { useCallback, useEffect } from 'react';
import { useParams, useSearchParams } from 'react-router';
import { FaChevronLeft } from 'react-icons/fa';
import PromotionExplorer from '@lib/components/PromotionExplorer/PromotionExplorer';
import type { CellSelection } from '@lib/components/PromotionExplorer/PromotionExplorer';
import { formatEnvSelection, parseEnvSelection } from '@lib/components/PromotionExplorer/helpers';
import HeaderBar from '@lib/components/HeaderBar';
import { LiveManifestView } from '@lib/components/LiveManifestView';
import type { PromotionStrategy } from '@shared/utils/PSData';
import {
  COMMIT_PARAM,
  ENV_PARAM,
  NODE_PARAM,
  VIEW_PARAM,
  useNavigateWithParams,
} from '../hooks/useNavigateWithParams';
import { namespaceStore } from '../stores/NamespaceStore';
import { PromotionStrategyStore } from '../stores/PromotionStrategyStore';
import './PromotionStrategyPage.scss';

interface NamespaceStore {
  namespace: string;
  setNamespace: (_ns: string) => void;
}

interface PromotionStrategyPageProps {
  namespace?: string;
  strategyName?: string;
}

const MANIFEST_VIEW = 'manifest';

const PromotionStrategyPage: React.FC<PromotionStrategyPageProps> = ({
  namespace: propsNamespace,
  strategyName: propsStrategyName,
}) => {
  const { namespace: urlNamespace, name: urlStrategyName } = useParams();
  const namespace = propsNamespace || urlNamespace;
  const strategyName = propsStrategyName || urlStrategyName;

  const currentNamespace = namespaceStore((s: NamespaceStore) => s.namespace);
  const setNamespace = namespaceStore((s: NamespaceStore) => s.setNamespace);

  const { items, fetchItems, subscribe, unsubscribe } = PromotionStrategyStore();

  const selectedStrategy = items.find((ps: PromotionStrategy) => ps.metadata.name === strategyName);

  const [searchParams, setSearchParams] = useSearchParams();
  const commit = searchParams.get(COMMIT_PARAM);
  const env = searchParams.get(ENV_PARAM);
  const initialSelection = commit && env ? { rowId: commit, branch: env } : null;
  const initialSelectedEnvs = parseEnvSelection(searchParams.get(NODE_PARAM));
  const showManifest = searchParams.get(VIEW_PARAM) === MANIFEST_VIEW;

  const updateParams = useCallback(
    (update: (_params: URLSearchParams) => void) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          update(next);
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const handleSelectionChange = useCallback(
    (selection: CellSelection | null) =>
      updateParams((params) => {
        if (selection) {
          params.set(COMMIT_PARAM, selection.rowId);
          params.set(ENV_PARAM, selection.branch);
        } else {
          params.delete(COMMIT_PARAM);
          params.delete(ENV_PARAM);
        }
      }),
    [updateParams],
  );

  const handleSelectedEnvsChange = useCallback(
    (branches: string[]) =>
      updateParams((params) => {
        const value = formatEnvSelection(branches);
        if (value) {
          params.set(NODE_PARAM, value);
        } else {
          params.delete(NODE_PARAM);
        }
      }),
    [updateParams],
  );

  const setView = useCallback(
    (manifest: boolean) =>
      updateParams((params) => {
        if (manifest) {
          params.set(VIEW_PARAM, MANIFEST_VIEW);
        } else {
          params.delete(VIEW_PARAM);
        }
      }),
    [updateParams],
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
    <div className="strategy-page">
      <div className="strategy-page-bar">
        <button
          type="button"
          className="strategy-page-back"
          aria-label="Back"
          onClick={() => navigate('/promotion-strategies')}
        >
          <FaChevronLeft size={14} />
          <span>Back</span>
        </button>

        <div className="strategy-page-tabs" role="tablist" aria-label="View">
          <button
            type="button"
            role="tab"
            aria-selected={!showManifest}
            className={`strategy-page-tab ${!showManifest ? 'active' : ''}`}
            onClick={() => setView(false)}
          >
            Explorer
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={showManifest}
            className={`strategy-page-tab ${showManifest ? 'active' : ''}`}
            onClick={() => setView(true)}
          >
            Live manifest
          </button>
        </div>
      </div>

      <div className="strategy-page-body">
        {showManifest ? (
          <>
            <HeaderBar name={strategyName || ''} />
            <LiveManifestView strategy={selectedStrategy} />
          </>
        ) : (
          <PromotionExplorer
            key={`${namespace}/${strategyName}`}
            strategy={selectedStrategy}
            name={strategyName}
            namespace={namespace}
            initialSelection={initialSelection}
            onSelectionChange={handleSelectionChange}
            initialSelectedEnvs={initialSelectedEnvs}
            onSelectedEnvsChange={handleSelectedEnvsChange}
          />
        )}
      </div>
    </div>
  );
};

export default PromotionStrategyPage;
