import { create } from 'zustand';

export type ViewMode = 'cards' | 'yaml';

interface ViewStore {
  currentView: ViewMode;
  setView: (view: ViewMode) => void;
}

export const viewStore = create<ViewStore>((set) => ({
  currentView: 'cards',
  setView: (view) => set({ currentView: view }),
})); 