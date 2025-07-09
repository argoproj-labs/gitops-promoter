import { create } from 'zustand';
import { persist } from 'zustand/middleware';

// Store for current namespace and list of namespaces
export const namespaceStore = create(persist<{
  namespace: string;
  namespaces: string[];
  setNamespace: (namespace: string) => void;
  setNamespaces: (namespaces: string[]) => void;
}>(
  (set) => ({
    namespace: '',
    namespaces: [],
    setNamespace: (namespace) => set({ namespace }),
    setNamespaces: (namespaces) => set({ namespaces }),
  }),
  {
    name: 'namespace-storage'
  }
)); 