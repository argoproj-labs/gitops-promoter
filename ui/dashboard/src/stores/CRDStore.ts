import { create } from 'zustand';
import { enrichFromCRD } from '@shared/utils/PSData';
import type { PromotionStrategy } from '@shared/utils/PSData';

interface CRDItem extends PromotionStrategy {
  enriched?: unknown;
}

export function createCRDStore<T extends CRDItem>(kind: string, eventName: string) {
    let eventSource: EventSource | null = null;

    return create<{
        items: T[];
        loading: boolean;
        error: string | null;
        connectionStatus?: 'connecting' | 'open' | 'error';
        fetchItems: (namespace: string) => Promise<void>;
        subscribe: (namespace: string) => void;
        unsubscribe: () => void;
        reset: () => void;
    }>
    
    ((set) => ({
        items: [],
        loading: false,
        error: null,
        connectionStatus: 'connecting',

        // Fetch items from via /list endpoint
        fetchItems: async (namespace: string) => {
            set({ loading: true, error: null });
            try {
                const res = await fetch(`/list?kind=${kind}&namespace=${namespace}`);

                if (!res.ok) throw new Error(`Error: ${res.status}`);
                const data = await res.json();
                
                set({ items: data, 
                    loading: false });

                    
            } catch (err: unknown) {
                const errorMessage = err instanceof Error ? err.message : 'Unknown error';
                set({ error: errorMessage, loading: false });
            }
        },

        // Subscribing to SSE
        subscribe: (namespace: string) => {
            if (eventSource) eventSource.close();

            // Real-Time fetch via /watch endpoint
            eventSource = new EventSource(`/watch?kind=${kind}&namespace=${namespace}`);

            // Handle PromotionStrategy SSE events
            eventSource.addEventListener(eventName, async (evt: MessageEvent) => {
                try {
                    const updated = JSON.parse(evt.data);
                    const enriched = enrichFromCRD(updated);
                    set((state) => {
                        const idx = state.items.findIndex(
                            (item: T) =>
                                item.metadata?.name === updated.metadata?.name &&
                                item.metadata?.namespace === updated.metadata?.namespace
                        );
                        let newItems: T[];
                        if (idx >= 0) {
                            newItems = [...state.items];
                            newItems[idx] = { ...updated, enriched };
                        } else {
                            newItems = [...state.items, { ...updated, enriched }];
                        }
                        return { items: newItems };
                    });
                } catch (err) {
                    set({ error: 'Failed to parse real-time update' });
                }
            });
        },

        // Unsubscribe from SSE
        unsubscribe: () => {
            if (eventSource) {
                eventSource.close();
                eventSource = null;
            }
        },

        // Reset items
        reset: () => set({ items: [] }),
    }));
} 