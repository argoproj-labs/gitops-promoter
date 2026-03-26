/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';

const makeStrategy = (name: string, namespace = 'default') =>
  JSON.stringify({
    kind: 'PromotionStrategy',
    apiVersion: 'promoter.argoproj.io/v1alpha1',
    metadata: {
      name,
      namespace,
      uid: 'uid-' + name,
      resourceVersion: '1',
      generation: 1,
      creationTimestamp: '',
    },
    spec: { gitRepositoryRef: { name: 'my-repo' }, environments: [] },
    status: { environments: [] },
  });

const makeTreeNode = (name: string, namespace = 'default', version = 'v1alpha1') => ({
  kind: 'PromotionStrategy',
  name,
  namespace,
  group: 'promoter.argoproj.io',
  version,
});

const makeProps = (nodes: ReturnType<typeof makeTreeNode>[]) => ({
  tree: { nodes },
  application: { metadata: { name: 'test-app', namespace: 'test-ns' } },
});

const wait = (ms = 100) => new Promise((resolve) => setTimeout(resolve, ms));

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
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);
vi.stubGlobal(
  'IntersectionObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);

describe('AppViewExtension', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    root?.unmount();
    container.remove();
    vi.restoreAllMocks();
  });

  const render = async (props: ReturnType<typeof makeProps>) => {
    const { default: AppViewExtension } = await import('./AppViewExtension');
    root = createRoot(container);
    root.render(React.createElement(AppViewExtension, props));
    await wait();
  };

  describe('when tree has no PromotionStrategy nodes', () => {
    it('shows an error message and does not call fetch', async () => {
      await render(makeProps([]));

      expect(vi.mocked(fetch)).not.toHaveBeenCalled();
      expect(container.textContent).toContain('No PromotionStrategy resources found');
    });

    it('ignores nodes with wrong group', async () => {
      const props = {
        tree: {
          nodes: [
            {
              kind: 'PromotionStrategy',
              name: 'ps',
              namespace: 'default',
              group: 'other.io',
              version: 'v1alpha1',
            },
          ],
        },
        application: { metadata: { name: 'test-app', namespace: 'test-ns' } },
      };

      await render(props);

      expect(vi.mocked(fetch)).not.toHaveBeenCalled();
      expect(container.textContent).toContain('No PromotionStrategy resources found');
    });

    it('ignores nodes with wrong kind', async () => {
      const props = {
        tree: {
          nodes: [
            {
              kind: 'Deployment',
              name: 'dep',
              namespace: 'default',
              group: 'promoter.argoproj.io',
              version: 'v1',
            },
          ],
        },
        application: { metadata: { name: 'test-app', namespace: 'test-ns' } },
      };

      await render(props);

      expect(vi.mocked(fetch)).not.toHaveBeenCalled();
    });
  });

  describe('dropdown with multiple strategies', () => {
    const setupTwoStrategies = async () => {
      vi.mocked(fetch)
        .mockResolvedValueOnce({
          ok: true,
          json: async () => ({ manifest: makeStrategy('strategy-1') }),
          text: async () => '',
        } as Response)
        .mockResolvedValueOnce({
          ok: true,
          json: async () => ({ manifest: makeStrategy('strategy-2') }),
          text: async () => '',
        } as Response);

      await render(makeProps([makeTreeNode('strategy-1'), makeTreeNode('strategy-2')]));
    };

    it('renders a strategy selector dropdown', async () => {
      await setupTwoStrategies();
      expect(container.querySelector('.strategy-dropdown__control')).not.toBeNull();
    });

    it('does not render a dropdown when only one strategy is loaded', async () => {
      vi.mocked(fetch).mockResolvedValue({
        ok: true,
        json: async () => ({ manifest: makeStrategy('only-strategy') }),
        text: async () => '',
      } as Response);

      await render(makeProps([makeTreeNode('only-strategy')]));

      expect(container.querySelector('.strategy-dropdown__control')).toBeNull();
    });

    it('initially displays the first strategy in the dropdown', async () => {
      await setupTwoStrategies();
      const singleValue = container.querySelector('.strategy-dropdown__single-value');
      expect(singleValue?.textContent).toBe('strategy-1');
    });

    it('shows all strategy names as options when opened', async () => {
      await setupTwoStrategies();

      container
        .querySelector('.strategy-dropdown__control')!
        .dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
      await wait();

      const options = Array.from(container.querySelectorAll('.strategy-dropdown__option'));
      const optionTexts = options.map((o) => o.textContent);
      expect(optionTexts).toContain('strategy-1');
      expect(optionTexts).toContain('strategy-2');
    });

    it('selecting a different option updates the displayed value and URL param', async () => {
      const replaceState = vi.spyOn(window.history, 'replaceState');
      await setupTwoStrategies();

      container
        .querySelector('.strategy-dropdown__control')!
        .dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
      await wait();

      const options = Array.from(container.querySelectorAll('.strategy-dropdown__option'));
      options
        .find((o) => o.textContent === 'strategy-2')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      expect(container.querySelector('.strategy-dropdown__single-value')?.textContent).toBe(
        'strategy-2',
      );
      const lastCall = replaceState.mock.calls[replaceState.mock.calls.length - 1];
      const url = new URL(lastCall[2] as string, 'http://localhost');
      expect(url.searchParams.get('promotionstrategy')).toBe('strategy-2');
    });
  });

  describe('when tree has PromotionStrategy nodes', () => {
    it('shows an error when fetch rejects', async () => {
      vi.mocked(fetch).mockRejectedValue(new Error('network failure'));

      await render(makeProps([makeTreeNode('my-strategy')]));

      expect(container.textContent).toContain('Failed to load PromotionStrategy');
      expect(container.textContent).toContain('network failure');
    });
  });
});
