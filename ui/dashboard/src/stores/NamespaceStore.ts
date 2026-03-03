import { create } from 'zustand';
import { persist } from 'zustand/middleware';

// Store for current namespace and list of namespaces
export const namespaceStore = create(persist<{
  namespace: string;
  namespaces: string[];
  setNamespace: (ns: string) => void;
  setNamespaces: (nsList: string[]) => void;
  addNamespace: (ns: string) => void;
}>(
  (set) => ({
    namespace: '',
    namespaces: [],
    setNamespace: (ns) => set({ namespace: ns }),
    setNamespaces: (nsList) => set({ namespaces: nsList }),
    addNamespace: (ns) => set((state) => (
      state.namespaces.includes(ns) ? state : { namespaces: [...state.namespaces, ns] }
    )),
  }),
  {
    name: 'namespace-storage'
  }
)); 