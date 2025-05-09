import { create } from 'zustand';
import { immer } from 'zustand/middleware/immer';
import type { PromotionStrategyType } from '../../../component-lib/src/models/promotionstrategy';

interface PromotionStrategyStore {
    strategies: PromotionStrategyType[];
    loading: boolean;
    error: string | null;
    fetchStrategies: () => Promise<void>;
    subscribeToStrategies: () => void;
    unsubscribeFromStrategies: () => void;
}

export const usePromotionStrategyStore = create(
    immer<PromotionStrategyStore>((set) => {
        let eventSource: EventSource | null = null;

        return {
            strategies: [],
            loading: false,
            error: null,

            async fetchStrategies() {
                set((state) => {
                    state.loading = true;
                    state.error = null;
                });
                try {
                    const response = await fetch('/list?kind=promotionstrategy');
                    if (!response.ok) throw new Error(`Request failed: ${response.status}`);
                    const data = await response.json();
                    set((state) => {
                        state.strategies = data;
                        state.loading = false;
                    });
                } catch (err: any) {
                    set((state) => {
                        state.error = err.message;
                        state.loading = false;
                    });
                }
            },

            subscribeToStrategies() {
                if (eventSource) eventSource.close();
                eventSource = new EventSource('/watch?kind=promotionstrategy&namespace=default');
                eventSource.addEventListener('PromotionStrategy', (evt) => {
                    try {
                        const updated = JSON.parse(evt.data);
                        set((state) => {
                            const idx = state.strategies.findIndex(
                                (s) => s.metadata.name === updated.metadata.name && s.metadata.namespace === updated.metadata.namespace
                            );
                            if (idx >= 0) {
                                state.strategies[idx] = updated; // Update existing strategy
                                console.log('Updated strategy:', updated);
                            } else {
                                state.strategies.push(updated); // Add new strategy
                                console.log('Added new strategy:', updated);
                            }
                        });
                    } catch (error) {
                        console.error('Error processing SSE event:', error);
                    }
                });
            },

            unsubscribeFromStrategies() {
                if (eventSource) {
                    eventSource.close();
                    eventSource = null;
                }
            },
        };
    })
);