import { namespaceStore } from '../../stores/NamespaceStore';
import React, { useEffect } from 'react';
import { PromotionStrategyStore } from '../../stores/PromotionStrategyStore';
import PromotionStrategiesTiles from '../../components/PromotionStrategySummary/PromotionStrategyTiles';

interface NamespaceStore {
  namespace: string;
  namespaces: string[];
  setNamespace: (namespace: string) => void;
  setNamespaces: (namespaces: string[]) => void;
}

export function PromotionStrategies() {
    
    const namespace = namespaceStore((s: NamespaceStore) => s.namespace);
    
    const { items, loading, error, fetchItems, subscribe, unsubscribe } = PromotionStrategyStore();

    useEffect(() => {
        if (!namespace) return;
        fetchItems(namespace);
        subscribe(namespace);
        return () => unsubscribe();
    }, [namespace]);

    if (!namespace) return null;
    if (loading) return <div>Loading...</div>;
    if (error) return <div>Error: {error}</div>;

    return (
        <div>
            <PromotionStrategiesTiles promotionStrategies={items || []} namespace={namespace} />

        </div>
    );
}