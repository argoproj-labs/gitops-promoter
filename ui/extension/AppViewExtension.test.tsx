/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';
import type { PromotionExplorerProps } from '@components-lib/components/PromotionExplorer/PromotionExplorer';

const makeStrategy = (name: string, namespace = 'default') =>
  JSON.stringify({
    kind: 'PromotionStrategyDetails',
    apiVersion: 'view.promoter.argoproj.io/v1alpha1',
    metadata: {
      name,
      namespace,
      uid: 'uid-' + name,
      resourceVersion: '1',
      generation: 1,
      creationTimestamp: '',
    },
    promotionStrategy: {
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
    },
    changeTransferPolicies: [],
  });

const makeTreeNode = (name: string, namespace = 'default', version = 'v1alpha1') => ({
  kind: 'PromotionStrategyDetails',
  name,
  namespace,
  group: 'view.promoter.argoproj.io',
  version,
});

const makeProps = (nodes: ReturnType<typeof makeTreeNode>[]) => ({
  tree: { nodes },
  application: { metadata: { name: 'test-app', namespace: 'test-ns' } },
});

const explorerProps = vi.hoisted(() => ({ current: null as PromotionExplorerProps | null }));

vi.mock('@components-lib/components/PromotionExplorer/PromotionExplorer', () => ({
  default: (props: PromotionExplorerProps) => {
    explorerProps.current = props;
    return React.createElement(
      'div',
      { 'data-testid': 'explorer' },
      React.createElement('button', {
        'data-testid': 'select',
        onClick: () => props.onSelectionChange?.({ rowId: 'abc123', branch: 'env/prod' }),
      }),
      React.createElement('button', {
        'data-testid': 'deselect',
        onClick: () => props.onSelectionChange?.(null),
      }),
      React.createElement('button', {
        'data-testid': 'pin',
        onClick: () => props.onSelectedEnvsChange?.(['env/staging']),
      }),
      React.createElement('button', {
        'data-testid': 'pin-many',
        onClick: () => props.onSelectedEnvsChange?.(['env/staging', 'env/prod']),
      }),
      React.createElement('button', {
        'data-testid': 'unpin',
        onClick: () => props.onSelectedEnvsChange?.([]),
      }),
    );
  },
}));

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
    explorerProps.current = null;
    window.history.replaceState(null, '', '/');
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

      const options = Array.from(document.querySelectorAll('.strategy-dropdown__option'));
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

      const options = Array.from(document.querySelectorAll('.strategy-dropdown__option'));
      options
        .find((o) => o.textContent === 'strategy-2')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      expect(container.querySelector('.strategy-dropdown__single-value')?.textContent).toBe(
        'strategy-2',
      );
      const lastCall = replaceState.mock.calls[replaceState.mock.calls.length - 1];
      const url = new URL(lastCall[2] as string, 'http://localhost');
      expect(url.searchParams.get('promotionstrategy')).toBe('default/strategy-2');
    });
  });

  describe('dropdown with duplicate strategy names across namespaces', () => {
    it('includes namespace in option labels when names collide', async () => {
      vi.mocked(fetch)
        .mockResolvedValueOnce({
          ok: true,
          json: async () => ({ manifest: makeStrategy('my-strategy', 'ns-a') }),
          text: async () => '',
        } as Response)
        .mockResolvedValueOnce({
          ok: true,
          json: async () => ({ manifest: makeStrategy('my-strategy', 'ns-b') }),
          text: async () => '',
        } as Response);

      await render(
        makeProps([makeTreeNode('my-strategy', 'ns-a'), makeTreeNode('my-strategy', 'ns-b')]),
      );

      container
        .querySelector('.strategy-dropdown__control')!
        .dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
      await wait();

      const options = Array.from(document.querySelectorAll('.strategy-dropdown__option'));
      const optionTexts = options.map((o) => o.textContent);
      expect(optionTexts).toContain('my-strategy (ns-a)');
      expect(optionTexts).toContain('my-strategy (ns-b)');
    });

    it('uses namespace/name as the URL param for duplicate names', async () => {
      const replaceState = vi.spyOn(window.history, 'replaceState');
      vi.mocked(fetch)
        .mockResolvedValueOnce({
          ok: true,
          json: async () => ({ manifest: makeStrategy('my-strategy', 'ns-a') }),
          text: async () => '',
        } as Response)
        .mockResolvedValueOnce({
          ok: true,
          json: async () => ({ manifest: makeStrategy('my-strategy', 'ns-b') }),
          text: async () => '',
        } as Response);

      await render(
        makeProps([makeTreeNode('my-strategy', 'ns-a'), makeTreeNode('my-strategy', 'ns-b')]),
      );

      container
        .querySelector('.strategy-dropdown__control')!
        .dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
      await wait();

      const options = Array.from(document.querySelectorAll('.strategy-dropdown__option'));
      options
        .find((o) => o.textContent === 'my-strategy (ns-b)')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      const lastCall = replaceState.mock.calls[replaceState.mock.calls.length - 1];
      const url = new URL(lastCall[2] as string, 'http://localhost');
      expect(url.searchParams.get('promotionstrategy')).toBe('ns-b/my-strategy');
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

  describe('promotion explorer', () => {
    const mockSingleStrategy = () =>
      vi.mocked(fetch).mockResolvedValue({
        ok: true,
        json: async () => ({ manifest: makeStrategy('only-strategy') }),
        text: async () => '',
      } as Response);

    const currentParams = () => new URLSearchParams(window.location.search);
    const clickTestId = async (id: string) => {
      (container.querySelector(`[data-testid="${id}"]`) as HTMLElement).click();
      await wait();
    };

    it('renders the explorer directly with no Overview/History toggle', async () => {
      mockSingleStrategy();

      await render(makeProps([makeTreeNode('only-strategy')]));

      expect(container.querySelector('[data-testid="explorer"]')).not.toBeNull();
      expect(container.querySelector('.gp-view-toggle')).toBeNull();
      expect(container.querySelector('[role="tablist"]')).toBeNull();
      expect(container.textContent).not.toContain('Overview');
      expect(container.textContent).not.toContain('History');
      expect(explorerProps.current?.strategy?.metadata.name).toBe('only-strategy');
    });

    it('starts with no selection or selected environments when the URL has none', async () => {
      mockSingleStrategy();

      await render(makeProps([makeTreeNode('only-strategy')]));

      expect(explorerProps.current?.initialSelection).toBeNull();
      expect(explorerProps.current?.initialSelectedEnvs).toEqual([]);
    });

    it('restores selection and selected environments from psCommit, psEnv and psNode', async () => {
      window.history.replaceState(
        null,
        '',
        '/?psCommit=abc123&psEnv=env%2Fprod&psNode=env%2Fstaging',
      );
      mockSingleStrategy();

      await render(makeProps([makeTreeNode('only-strategy')]));

      expect(explorerProps.current?.initialSelection).toEqual({
        rowId: 'abc123',
        branch: 'env/prod',
      });
      expect(explorerProps.current?.initialSelectedEnvs).toEqual(['env/staging']);
    });

    it('restores several selected environments from a comma separated psNode', async () => {
      window.history.replaceState(null, '', '/?psNode=env%2Fstaging%2Cenv%2Fprod');
      mockSingleStrategy();

      await render(makeProps([makeTreeNode('only-strategy')]));

      expect(explorerProps.current?.initialSelectedEnvs).toEqual(['env/staging', 'env/prod']);
    });

    it('ignores psCommit without psEnv', async () => {
      window.history.replaceState(null, '', '/?psCommit=abc123');
      mockSingleStrategy();

      await render(makeProps([makeTreeNode('only-strategy')]));

      expect(explorerProps.current?.initialSelection).toBeNull();
    });

    it('writes selection and selected environments to the URL and clears them', async () => {
      mockSingleStrategy();
      await render(makeProps([makeTreeNode('only-strategy')]));

      await clickTestId('select');
      expect(currentParams().get('psCommit')).toBe('abc123');
      expect(currentParams().get('psEnv')).toBe('env/prod');

      await clickTestId('pin');
      expect(currentParams().get('psNode')).toBe('env/staging');
      expect(currentParams().get('psCommit')).toBe('abc123');
      expect(currentParams().get('promotionstrategy')).toBe('default/only-strategy');

      await clickTestId('pin-many');
      expect(currentParams().get('psNode')).toBe('env/staging,env/prod');

      await clickTestId('unpin');
      expect(currentParams().has('psNode')).toBe(false);

      await clickTestId('deselect');
      expect(currentParams().has('psCommit')).toBe(false);
      expect(currentParams().has('psEnv')).toBe(false);
    });

    it('clears selection and selected environments when switching strategies', async () => {
      window.history.replaceState(
        null,
        '',
        '/?psCommit=abc123&psEnv=env%2Fprod&psNode=env%2Fstaging',
      );
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

      container
        .querySelector('.strategy-dropdown__control')!
        .dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
      await wait();
      Array.from(document.querySelectorAll('.strategy-dropdown__option'))
        .find((o) => o.textContent === 'strategy-2')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      expect(currentParams().get('promotionstrategy')).toBe('default/strategy-2');
      expect(currentParams().has('psCommit')).toBe(false);
      expect(currentParams().has('psEnv')).toBe(false);
      expect(currentParams().has('psNode')).toBe(false);
      expect(explorerProps.current?.strategy?.metadata.name).toBe('strategy-2');
    });
  });
});
