import { create } from 'zustand';
import { enrichFromCRD } from '@shared/utils/PSData';
import { mergePromotionStrategyFromBundle } from '@shared/utils/bundleToUI';
import type { PromotionStrategy } from '@shared/utils/PSData';
import type { PromotionStrategyDetails, GitRepository, ScmProvider, ClusterScmProvider } from '@shared/types/view';

interface CRDItem extends PromotionStrategy {
  enriched?: unknown;
  gitRepository?: GitRepository;
  scmProvider?: ScmProvider;
  clusterScmProvider?: ClusterScmProvider;
}

function bundleToItem<T extends CRDItem>(bundle: PromotionStrategyDetails): T {
  const { promotionStrategy, gitRepository, scmProvider, clusterScmProvider } =
    mergePromotionStrategyFromBundle(bundle);
  return {
    ...promotionStrategy,
    gitRepository,
    scmProvider,
    clusterScmProvider,
    enriched: enrichFromCRD(promotionStrategy, 0, { gitRepository, scmProvider, clusterScmProvider }),
  } as T;
}

export function createCRDStore<T extends CRDItem>(kind: string, eventName: string) {
  let eventSource: EventSource | null = null;

  return create<{
    items: T[];
    loading: boolean;
    error: string | null;
    connectionStatus?: 'connecting' | 'open' | 'error';
    fetchItems: (_ns: string) => Promise<void>;
    subscribe: (_ns: string) => void;
    unsubscribe: () => void;
    reset: () => void;
  }>((set) => ({
    items: [],
    loading: false,
    error: null,
    connectionStatus: 'connecting',

    fetchItems: async (namespace: string) => {
      set({ loading: true, error: null });

      try {
        const res = await fetch(`/list?kind=${kind}&namespace=${namespace}`);

        if (!res.ok) throw new Error(`Error: ${res.status}`);
        const data = (await res.json()) as PromotionStrategyDetails[] | null;

        set({ items: (data ?? []).map((b) => bundleToItem<T>(b)), loading: false });
      } catch (err: unknown) {
        const errorMessage = err instanceof Error ? err.message : 'Unknown error';
        set({ error: errorMessage, loading: false });
      }
    },

    subscribe: (namespace: string) => {
      if (eventSource) eventSource.close();

      eventSource = new EventSource(`/watch?kind=${kind}&namespace=${namespace}`);

      eventSource.addEventListener(eventName, async (evt: MessageEvent) => {
        try {
          const bundle: PromotionStrategyDetails = JSON.parse(evt.data);
          const updated = bundleToItem<T>(bundle);
          set((state) => {
            const idx = state.items.findIndex(
              (item: T) =>
                item.metadata.name === updated.metadata.name &&
                item.metadata.namespace === updated.metadata.namespace,
            );
            let newItems: T[];
            if (idx >= 0) {
              newItems = [...state.items];
              newItems[idx] = updated;
            } else {
              newItems = [...state.items, updated];
            }
            return { items: newItems };
          });
        } catch {
          set({ error: 'Failed to parse real-time update' });
        }
      });
    },

    unsubscribe: () => {
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
    },

    reset: () => set({ items: [] }),
  }));
}
