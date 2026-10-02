import { beforeEach, describe, expect, it } from 'vitest';
import { namespaceStore } from '../src/stores/NamespaceStore';

describe('namespaceStore', () => {
  beforeEach(() => {
    namespaceStore.setState({ namespace: '', namespaces: [] });
  });

  describe('setNamespaces', () => {
    it('sorts namespaces alphabetically', () => {
      namespaceStore.getState().setNamespaces(['zebra', 'alpha', 'middle']);

      expect(namespaceStore.getState().namespaces).toEqual(['alpha', 'middle', 'zebra']);
    });

    it('does not mutate the input array', () => {
      const input = ['zebra', 'alpha'];
      namespaceStore.getState().setNamespaces(input);

      expect(input).toEqual(['zebra', 'alpha']);
    });
  });
});
