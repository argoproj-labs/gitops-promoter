import { create } from 'zustand';
import { enrichFromCRD } from '@shared/utils/PSData';
import type { PromotionStrategy } from '@shared/utils/PSData';

interface CRDItem extends PromotionStrategy {
  enriched?: unknown;
}

interface CRDStoreState<T extends CRDItem> {
  items: T[];
  loading: boolean;
  error: string | null;
  connectionStatus: 'connecting' | 'open' | 'error' | 'closed';
}

interface CRDStoreActions {
  fetchItems: (namespace: string) => Promise<void>;
  subscribe: (namespace: string) => void;
  unsubscribe: () => void;
  reset: () => void;
}

type CRDStore<T extends CRDItem> = CRDStoreState<T> & CRDStoreActions;

export function createCRDStore<T extends CRDItem>(
  kind: string,
  eventName: string
) {
  let eventSource: EventSource | null = null;

  return create<CRDStore<T>>((set, get) => ({
    // Initial state
    items: [],
    loading: false,
    error: null,
    connectionStatus: 'connecting',

    // Fetch items via /list endpoint
    fetchItems: async (namespace: string): Promise<void> => {
      set({ loading: true, error: null });

      try {
        const response = await fetch(`/list?kind=${kind}&namespace=${namespace}`);

        if (!response.ok) {
          throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();

        set({
          items: data,
          loading: false,
          error: null
        });
      } catch (error) {
        const errorMessage = error instanceof Error
          ? error.message
          : 'Failed to fetch items';

        set({
          error: errorMessage,
          loading: false
        });
      }
    },

    // Subscribe to real-time updates via SSE
    subscribe: (namespace: string): void => {
      // Clean up existing connection
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }

      try {
        // Establish new SSE connection
        eventSource = new EventSource(`/watch?kind=${kind}&namespace=${namespace}`);

        eventSource.onopen = () => {
          set({ connectionStatus: 'open', error: null });
        };

        eventSource.onerror = () => {
          set({
            connectionStatus: 'error',
            error: 'Connection to server lost'
          });
        };

        // Handle specific PromotionStrategy SSE events
        eventSource.addEventListener(eventName, (event: MessageEvent) => {
          try {
            const updated = JSON.parse(event.data) as T;
            const enriched = enrichFromCRD(updated);

            set((state) => {
              const existingItemIndex = state.items.findIndex(
                (item) =>
                  item.metadata?.name === updated.metadata?.name &&
                  item.metadata?.namespace === updated.metadata?.namespace
              );

              let updatedItems: T[];

              if (existingItemIndex >= 0) {
                // Update existing item
                updatedItems = [...state.items];
                updatedItems[existingItemIndex] = {
                  ...updated,
                  enriched
                } as T;
              } else {
                // Add new item
                updatedItems = [
                  ...state.items,
                  { ...updated, enriched } as T
                ];
              }

              return {
                items: updatedItems,
                error: null
              };
            });
          } catch (parseError) {
            console.error('Failed to parse SSE event:', parseError);
            set({ error: 'Failed to parse real-time update' });
          }
        });
      } catch (connectionError) {
        console.error('Failed to establish SSE connection:', connectionError);
        set({
          connectionStatus: 'error',
          error: 'Failed to establish real-time connection'
        });
      }
    },

    // Unsubscribe from SSE
    unsubscribe: (): void => {
      if (eventSource) {
        eventSource.close();
        eventSource = null;
        set({ connectionStatus: 'closed' });
      }
    },

    // Reset store state
    reset: (): void => {
      set({
        items: [],
        loading: false,
        error: null,
        connectionStatus: 'connecting'
      });
    },
  }));
}
