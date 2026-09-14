// Node 22 ships an experimental localStorage global that shadows jsdom's and
// exposes no Storage methods. The zustand persist middleware captures it at
// import time, so it has to be replaced before any test module is imported.
const store = new Map<string, string>();

Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  writable: true,
  value: {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, String(value)),
    removeItem: (key: string) => void store.delete(key),
    clear: () => store.clear(),
    key: (index: number) => [...store.keys()][index] ?? null,
    get length() {
      return store.size;
    },
  },
});
