import React, { useCallback, useEffect } from 'react';
import { useSearchParams } from 'react-router';
import { namespaceStore } from '../stores/NamespaceStore';
import { NamespaceDropdown } from '../components/NamespaceDropdown';
import { PromotionStrategies } from '../features/promotion/PromotionStrategies';
import { resolveNamespace } from './resolveNamespace';
import './DashboardPage.scss';

interface NamespaceStore {
  namespace: string;
  namespaces: string[];
  setNamespace: (_ns: string) => void;
}

const DashboardPage: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams();
  const persistedNamespace = namespaceStore((s: NamespaceStore) => s.namespace);
  const namespaces = namespaceStore((s: NamespaceStore) => s.namespaces);
  const setNamespace = namespaceStore((s: NamespaceStore) => s.setNamespace);

  const namespace = resolveNamespace(searchParams.get('namespace'), persistedNamespace, namespaces);

  useEffect(() => {
    if (namespace !== persistedNamespace) {
      setNamespace(namespace);
    }
  }, [namespace, persistedNamespace, setNamespace]);

  const handleNamespaceChange = useCallback(
    (next: string) => {
      setNamespace(next);
      setSearchParams((prev) => {
        const params = new URLSearchParams(prev);
        if (next) {
          params.set('namespace', next);
        } else {
          params.delete('namespace');
        }
        return params;
      });
    },
    [setNamespace, setSearchParams],
  );

  return (
    <>
      <div className="dashboard-main">
        <div className="dashboard-namespace-dropdown-wrapper">
          <NamespaceDropdown namespace={namespace} onNamespaceChange={handleNamespaceChange} />
        </div>
        {namespace && (
          <div className="dashboard-content-card">
            <PromotionStrategies />
          </div>
        )}
      </div>
    </>
  );
};

export default DashboardPage;
