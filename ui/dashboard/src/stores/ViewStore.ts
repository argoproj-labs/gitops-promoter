import { create } from 'zustand';

export type ViewMode = 'cards' | 'yaml';

interface ViewStore {
  currentView: ViewMode;
  setView: (_mode: ViewMode) => void;
}

export const viewStore = create<ViewStore>((set) => ({
  currentView: 'cards',
  setView: (viewMode) => set({ currentView: viewMode }),
})); 