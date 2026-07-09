import { create } from 'zustand';
import { enrichFromCRD } from '@shared/utils/PSData';
import type { PromotionStrategy } from '@shared/utils/PSData';
import type { Environment } from '@shared/types/promotion';
import type { ChangeTransferPolicy, PromotionStrategyDetails } from '@shared/types/view';

interface CRDItem extends PromotionStrategy {
  enriched?: unknown;
}

// Reconstruct the per-environment status the UI renders. The bundle no longer carries
// the PromotionStrategy status (it was a duplicate aggregation); instead we build the
// environment list from the embedded ChangeTransferPolicies, one per environment keyed
// by spec.activeBranch, in the order declared by the PromotionStrategy spec.
function environmentsFromCTPs(
  spec: PromotionStrategy['spec'],
  ctps: ChangeTransferPolicy[],
): Environment[] {
  const byBranch = new Map<string, ChangeTransferPolicy>();
  for (const ctp of ctps) {
    const branch = ctp.spec?.activeBranch;
    if (branch) byBranch.set(branch, ctp);
  }

  return spec.environments.map((env) => {
    const status = byBranch.get(env.branch)?.status ?? {};
    return {
      branch: env.branch,
      active: status.active ?? { dry: {}, hydrated: {} },
      proposed: status.proposed ?? { dry: {}, hydrated: {} },
      pullRequest: status.pullRequest,
      history: status.history,
      lastHealthyDryShas: [],
    };
  });
}

function bundleToItem<T extends CRDItem>(bundle: PromotionStrategyDetails): T {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromCTPs(ps.spec, bundle.changeTransferPolicies ?? []);
  const psWithEnvironments = {
    ...ps,
    metadata: {
      ...ps.metadata,
      name: bundle.metadata.name,
      namespace: bundle.metadata.namespace,
    },
    status: { ...ps.status, environments },
  } as PromotionStrategy;
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
