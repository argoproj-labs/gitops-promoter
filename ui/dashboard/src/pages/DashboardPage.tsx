import React from 'react';
import { namespaceStore } from '../stores/NamespaceStore';
import { NamespaceDropdown } from '../components/NamespaceDropdown';
import { PromotionStrategies } from '../features/promotion/PromotionStrategies';
import './DashboardPage.scss';

interface NamespaceStore {
  namespace: string;
  namespaces: string[];
  setNamespace: (namespace: string) => void;
  setNamespaces: (namespaces: string[]) => void;
}

const DashboardPage: React.FC = () => {
  const namespace = namespaceStore((s: NamespaceStore) => s.namespace);
  
  
  
  return (
    <>
      <div className="dashboard-main">
        <div className="dashboard-namespace-dropdown-wrapper">
          <NamespaceDropdown />
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