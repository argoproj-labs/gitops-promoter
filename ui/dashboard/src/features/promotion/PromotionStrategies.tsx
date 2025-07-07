import {useEffect} from 'react'
import { namespaceStore } from '@shared/stores/NamespaceStore';
import PromotionStrategiesTiles from '../../components/PromotionStrategySummary/PromotionStrategyTiles';
import { PromotionStrategyStore } from '@shared/stores/PromotionStrategyStore';

export function PromotionStrategies() {
    
    const namespace = namespaceStore((s: any) => s.namespace);
    
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