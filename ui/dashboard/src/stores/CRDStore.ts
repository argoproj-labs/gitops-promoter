import { create } from 'zustand';
import { mergeCommitStatusManagers } from '@shared/utils/PSData';
import { environmentsFromBundle } from '@shared/utils/environments';
import { scmProviderSpecFromBundle, sortStrategyCommitStatuses } from '@shared/utils/util';
import type { PromotionStrategy, CommitStatusManagerBundle } from '@shared/utils/PSData';
import type { PromotionStrategyDetails, ScmProviderSpec } from '@shared/types/view';

type CRDItem = PromotionStrategy;

function managersFromBundle(bundle: PromotionStrategyDetails): CommitStatusManagerBundle {
  return {
    timedCommitStatuses: bundle.timedCommitStatuses,
    gitCommitStatuses: bundle.gitCommitStatuses,
    scheduledCommitStatuses: bundle.scheduledCommitStatuses,
    argoCDCommitStatuses: bundle.argoCDCommitStatuses,
    webRequestCommitStatuses: bundle.webRequestCommitStatuses,
    dependentsSuccessfulCommitStatuses: bundle.dependentsSuccessfulCommitStatuses,
  };
}

function bundleToItem<T extends CRDItem>(bundle: PromotionStrategyDetails): T {
  const ps = bundle.promotionStrategy;
  const environments = environmentsFromBundle(
    ps.spec,
    bundle.changeTransferPolicies ?? [],
    bundle.changeTransferPolicyHistories ?? [],
    bundle.restoreActiveCommits ?? [],
  );
  const psWithEnvironments = {
    ...ps,
    metadata: {
      ...ps.metadata,
      name: bundle.metadata.name,
      namespace: bundle.metadata.namespace,
    },
    status: { ...ps.status, environments },
  } as PromotionStrategy;
  sortStrategyCommitStatuses(psWithEnvironments);
  return mergeCommitStatusManagers(psWithEnvironments, managersFromBundle(bundle)) as T;
}

function itemKey(item: { metadata: { namespace?: string; name?: string } }): string {
  return `${item.metadata.namespace}/${item.metadata.name}`;
}

export function createCRDStore<T extends CRDItem>(kind: string, eventName: string) {
  let eventSource: EventSource | null = null;

  return create<{
    items: T[];
    // The SCM provider spec of each item's bundle, keyed by namespace/name. Kept off the items so
    // they stay the PromotionStrategy the manifest view shows.
    scmProviders: Record<string, ScmProviderSpec | undefined>;
    loading: boolean;
    error: string | null;
    connectionStatus?: 'connecting' | 'open' | 'error';
    fetchItems: (_ns: string) => Promise<void>;
    subscribe: (_ns: string) => void;
    unsubscribe: () => void;
    reset: () => void;
  }>((set) => ({
    items: [],
    scmProviders: {},
    loading: false,
    error: null,
    connectionStatus: 'connecting',

    fetchItems: async (namespace: string) => {
      set({ loading: true, error: null });

      try {
        const res = await fetch(`/list?kind=${kind}&namespace=${namespace}`);

        if (!res.ok) throw new Error(`Error: ${res.status}`);
        const data = (await res.json()) as PromotionStrategyDetails[] | null;

        const bundles = data ?? [];
        set({
          items: bundles.map((b) => bundleToItem<T>(b)),
          scmProviders: Object.fromEntries(
            bundles.map((b) => [itemKey(b), scmProviderSpecFromBundle(b)]),
          ),
          loading: false,
        });
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
            return {
              items: newItems,
              scmProviders: {
                ...state.scmProviders,
                [itemKey(bundle)]: scmProviderSpecFromBundle(bundle),
              },
            };
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

    reset: () => set({ items: [], scmProviders: {} }),
  }));
}
