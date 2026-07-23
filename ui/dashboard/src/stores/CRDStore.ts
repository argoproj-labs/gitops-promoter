import { create } from 'zustand';
import { enrichFromCRD } from '@shared/utils/PSData';
import { sortStrategyCommitStatuses } from '@shared/utils/util';
import { bundleToPromotionStrategy } from '@shared/utils/bundle';
import { isMockMode } from '@shared/fixtures/mockMode';
import type { PromotionStrategy } from '@shared/utils/PSData';
import type { PromotionStrategyDetails } from '@shared/types/view';

interface CRDItem extends PromotionStrategy {
  enriched?: unknown;
}

// The mock fixture is PromotionStrategy bundles, so only serve it to that store.
const PROMOTION_STRATEGY_KIND = 'PromotionStrategyDetails';

// Gated on import.meta.env.DEV so the prod build constant-folds this to `false`,
// letting Rollup drop the branch and the dynamic mockData chunk entirely.
const mockEnabled = (kind: string) =>
  import.meta.env.DEV && isMockMode() && kind === PROMOTION_STRATEGY_KIND;

function bundleToItem<T extends CRDItem>(bundle: PromotionStrategyDetails): T {
  const psWithEnvironments = bundleToPromotionStrategy(bundle);
  sortStrategyCommitStatuses(psWithEnvironments);
  return {
    ...psWithEnvironments,
    enriched: enrichFromCRD(psWithEnvironments),
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
      if (mockEnabled(kind)) {
        const { mockBundles } = await import('@shared/fixtures/mockData');
        set({
          items: mockBundles().map((b) => bundleToItem<T>(b)),
          loading: false,
          error: null,
          connectionStatus: 'open',
        });
        return;
      }

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
      // No live stream in mock mode; the fixture is static.
      if (mockEnabled(kind)) return;

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
