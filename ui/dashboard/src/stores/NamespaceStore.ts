import { create } from 'zustand';
import { persist } from 'zustand/middleware';

// Store for current namespace and list of namespaces
export const namespaceStore = create(persist<{
  namespace: string;
  namespaces: string[];
  setNamespace: (ns: string) => void;
  setNamespaces: (nsList: string[]) => void;
}>(
  (set) => ({
    namespace: '',
    namespaces: [],
    setNamespace: (ns) => set({ namespace: ns }),
    setNamespaces: (nsList) => set({ namespaces: nsList }),
  }),
  {
    name: 'namespace-storage'
  }
)); 