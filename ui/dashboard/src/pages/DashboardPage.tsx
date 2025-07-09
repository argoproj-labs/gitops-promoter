import React, { useEffect } from 'react';
import { Link } from 'react-router-dom';
import { namespaceStore } from '../stores/NamespaceStore';
import { NamespaceDropdown } from '../components/NamespaceDropdown';
import { PromotionStrategies } from '../features/promotion/PromotionStrategies';
import './DashboardPage.scss';


const DashboardPage: React.FC = () => {
  const namespace = namespaceStore((s: any) => s.namespace);
  
  
  
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