import { create } from 'zustand';
import { enrichFromCRD } from '@shared/utils/PSData';
import { environmentsFromCTPs, repoURLFromBundle } from '@shared/utils/bundle';
import type { PromotionStrategy } from '@shared/utils/PSData';
import type { PromotionStrategyBundle } from '@shared/types/bundle';

interface CRDItem extends PromotionStrategy {
  enriched?: unknown;
}

// The dashboard consumes a single, server-computed PromotionStrategyDetails bundle
// (group view.promoter.argoproj.io) instead of four raw CRD streams. The bundle
// embeds the PromotionStrategy (metadata + spec only) plus its ChangeTransferPolicies;
// we rebuild status.environments from those CTPs so the existing enrichment/components
// keep working.
function bundleToItem<T extends CRDItem>(bundle: PromotionStrategyBundle): T {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromCTPs(ps, bundle.changeTransferPolicies ?? []);
  const deploymentRepoURL = repoURLFromBundle(bundle);
  const psWithEnvironments: PromotionStrategy = {
    ...ps,
    status: { ...ps.status, environments },
  };
  return {
    ...psWithEnvironments,
    enriched: enrichFromCRD(psWithEnvironments, 0, deploymentRepoURL),
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

    // Fetch the current set of bundles via the /list endpoint.
    fetchItems: async (namespace: string) => {
      set({ loading: true, error: null });
      try {
        const res = await fetch(`/list?kind=${kind}&namespace=${namespace}`);

        if (!res.ok) throw new Error(`Error: ${res.status}`);
        const data: PromotionStrategyBundle[] = await res.json();

        set({ items: (data || []).map((b) => bundleToItem<T>(b)), loading: false });
      } catch (err: unknown) {
        const errorMessage = err instanceof Error ? err.message : 'Unknown error';
        set({ error: errorMessage, loading: false });
      }
    },

    // Subscribe to bundle updates over SSE.
    subscribe: (namespace: string) => {
      if (eventSource) eventSource.close();

      eventSource = new EventSource(`/watch?kind=${kind}&namespace=${namespace}`);

      eventSource.addEventListener(eventName, async (evt: MessageEvent) => {
        try {
          const bundle: PromotionStrategyBundle = JSON.parse(evt.data);
          const updated = bundleToItem<T>(bundle);
          set((state) => {
            const idx = state.items.findIndex(
              (item: T) =>
                item.metadata?.name === updated.metadata?.name &&
                item.metadata?.namespace === updated.metadata?.namespace,
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
