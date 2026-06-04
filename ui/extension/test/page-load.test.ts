/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';

// Mock fetch globally
vi.stubGlobal(
  'fetch',
  vi.fn(() =>
    Promise.resolve({
      ok: true,
      json: () => Promise.resolve({}),
      text: () => Promise.resolve(''),
    }),
  ),
);

// Mock matchMedia
vi.stubGlobal(
  'matchMedia',
  vi.fn((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(() => true),
  })),
);

// Mock ResizeObserver
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);

// Mock IntersectionObserver
vi.stubGlobal(
  'IntersectionObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);

describe('Extension Page Load Tests', () => {
  let container: HTMLDivElement;

  beforeEach(() => {
    container = document.createElement('div');
    container.id = 'test-root';
    document.body.appendChild(container);
  });

  afterEach(() => {
    if (container && container.parentNode) {
      container.parentNode.removeChild(container);
    }
  });

  describe('AppViewExtension Component', () => {
    it('should render without crashing when no PromotionStrategy nodes exist', async () => {
      const { default: AppViewExtension } = await import('../AppViewExtension');

      const props = {
        tree: { nodes: [] },
        application: {
          metadata: { name: 'test-app', namespace: 'default' },
        },
      };

      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(AppViewExtension, props));
      }).not.toThrow();

      root.unmount();
    });

    it('should render without crashing when one PromotionStrategy node exists', async () => {
      const { default: AppViewExtension } = await import('../AppViewExtension');

      const props = {
        tree: {
          nodes: [
            {
              kind: 'PromotionStrategy',
              name: 'my-strategy',
              namespace: 'default',
              group: 'promoter.argoproj.io',
              version: 'v1alpha1',
            },
          ],
        },
        application: {
          metadata: { name: 'test-app', namespace: 'default' },
        },
      };

      const root = createRoot(container);
      root.render(React.createElement(AppViewExtension, props));

      // Wait for render
      await new Promise((resolve) => setTimeout(resolve, 100));

      expect(container.innerHTML).not.toBe('');

      root.unmount();
    });

    it('should render without crashing when multiple PromotionStrategy nodes exist', async () => {
      const { default: AppViewExtension } = await import('../AppViewExtension');

      const props = {
        tree: {
          nodes: [
            {
              kind: 'PromotionStrategy',
              name: 'strategy-1',
              namespace: 'default',
              group: 'promoter.argoproj.io',
              version: 'v1alpha1',
            },
            {
              kind: 'PromotionStrategy',
              name: 'strategy-2',
              namespace: 'default',
              group: 'promoter.argoproj.io',
              version: 'v1alpha1',
            },
          ],
        },
        application: {
          metadata: { name: 'test-app', namespace: 'default' },
        },
      };

      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(AppViewExtension, props));
      }).not.toThrow();

      root.unmount();
    });
  });

  describe('ResourceExtension Component', () => {
    it('should render without crashing with an empty resource', async () => {
      const { default: ResourceExtension } = await import('../resourceExtension');

      const props = {
        application: {
          metadata: { name: 'test-app', namespace: 'default' },
        },
        resource: {
          kind: 'PromotionStrategy',
          apiVersion: 'promoter.argoproj.io/v1alpha1',
          metadata: {
            name: 'my-strategy',
            namespace: 'default',
            uid: 'test-uid',
            resourceVersion: '1',
            generation: 1,
            creationTimestamp: '2024-01-01T00:00:00Z',
          },
          spec: {
            gitRepositoryRef: { name: 'my-repo' },
            environments: [],
          },
        },
      };

      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(ResourceExtension, props));
      }).not.toThrow();

      root.unmount();
    });

    it('should render without crashing with status environments', async () => {
      const { default: ResourceExtension } = await import('../resourceExtension');

      const props = {
        application: {
          metadata: { name: 'test-app', namespace: 'default' },
        },
        resource: {
          kind: 'PromotionStrategy',
          apiVersion: 'promoter.argoproj.io/v1alpha1',
          metadata: {
            name: 'my-strategy',
            namespace: 'default',
            uid: 'test-uid',
            resourceVersion: '1',
            generation: 1,
            creationTimestamp: '2024-01-01T00:00:00Z',
          },
          spec: {
            gitRepositoryRef: { name: 'my-repo' },
            environments: [{ branch: 'main' }],
          },
          status: {
            environments: [
              {
                branch: 'main',
                active: {},
                proposed: {},
              },
            ],
          },
        },
      };

      const root = createRoot(container);
      root.render(React.createElement(ResourceExtension, props));

      // Wait for render
      await new Promise((resolve) => setTimeout(resolve, 100));

      expect(container.innerHTML).not.toBe('');

      root.unmount();
    });
  });
});
