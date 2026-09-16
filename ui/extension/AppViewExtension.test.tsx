/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';

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

const DEV_BRANCH = 'environments/dev';
const PRD_BRANCH = 'environments/prd';

const makeCommit = (sha: string, subject: string, commitTime: string) => ({
  sha,
  subject,
  author: 'deployment-bot <bot@example.com>',
  repoURL: 'https://github.example.com/deployment',
  commitTime,
});

const NEWER_COMMIT = makeCommit(
  'aaaaaaa1111111111111111111111111111111111',
  'add new feature',
  '2026-05-22T15:00:00Z',
);
const OLDER_COMMIT = makeCommit(
  'bbbbbbb2222222222222222222222222222222222',
  'fix a bug',
  '2026-05-22T14:00:00Z',
);

const makeCTP = (branch: string, status: Record<string, unknown>) => ({
  kind: 'ChangeTransferPolicy',
  apiVersion: 'promoter.argoproj.io/v1alpha1',
  metadata: { name: branch, namespace: 'default' },
  spec: { activeBranch: branch, activeCommitStatuses: [], proposedBranch: branch, proposedCommitStatuses: [] },
  status,
});

const makeStrategyWithHistory = (name: string, namespace = 'default') =>
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
      spec: {
        gitRepositoryRef: { name: 'my-repo' },
        environments: [{ branch: DEV_BRANCH }, { branch: PRD_BRANCH }],
      },
      status: { environments: [] },
    },
    changeTransferPolicies: [
      makeCTP(DEV_BRANCH, {
        active: {
          dry: NEWER_COMMIT,
          hydrated: {},
          commitStatuses: [{ key: 'ci', phase: 'success' }],
        },
        proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
        history: [
          {
            active: {
              dry: NEWER_COMMIT,
              hydrated: {},
              commitStatuses: [{ key: 'ci', phase: 'success' }],
            },
          },
          {
            active: {
              dry: OLDER_COMMIT,
              hydrated: {},
              commitStatuses: [{ key: 'ci', phase: 'failure' }],
            },
          },
        ],
      }),
      makeCTP(PRD_BRANCH, {
        active: {
          dry: OLDER_COMMIT,
          hydrated: {},
          commitStatuses: [{ key: 'ci', phase: 'success' }],
        },
        proposed: { dry: {}, hydrated: {}, commitStatuses: [] },
        history: [
          {
            active: {
              dry: OLDER_COMMIT,
              hydrated: {},
              commitStatuses: [{ key: 'ci', phase: 'success' }],
            },
          },
        ],
      }),
    ],
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

  describe('view tab deep linking', () => {
    const setSearch = (search: string) => {
      window.history.replaceState(null, '', '/applications/test-app' + search);
    };

    const renderOneStrategy = async () => {
      vi.mocked(fetch).mockResolvedValue({
        ok: true,
        json: async () => ({ manifest: makeStrategy('only-strategy') }),
        text: async () => '',
      } as Response);

      await render(makeProps([makeTreeNode('only-strategy')]));
    };

    const tab = (label: string) =>
      Array.from(container.querySelectorAll('[role="tab"]')).find(
        (t) => t.textContent === label,
      ) as HTMLButtonElement;

    const lastSearchParams = (replaceState: ReturnType<typeof vi.spyOn>) => {
      const calls = replaceState.mock.calls;
      const url = new URL(calls[calls.length - 1][2] as string, 'http://localhost');
      return url.searchParams;
    };

    afterEach(() => {
      setSearch('');
    });

    it('opens the history tab from psView with no selection present', async () => {
      setSearch('?psView=history');
      await renderOneStrategy();

      expect(tab('History').getAttribute('aria-selected')).toBe('true');
      expect(tab('Overview').getAttribute('aria-selected')).toBe('false');
    });

    it('opens the card tab when psView is absent', async () => {
      setSearch('');
      await renderOneStrategy();

      expect(tab('Overview').getAttribute('aria-selected')).toBe('true');
      expect(tab('History').getAttribute('aria-selected')).toBe('false');
    });

    it('opens the card tab for an unrecognized psView value', async () => {
      setSearch('?psView=bogus');
      await renderOneStrategy();

      expect(tab('Overview').getAttribute('aria-selected')).toBe('true');
    });

    it('still infers the history tab from a shipped psCommit/psEnv link', async () => {
      setSearch('?psCommit=abc123&psEnv=main');
      await renderOneStrategy();

      expect(tab('History').getAttribute('aria-selected')).toBe('true');
    });

    it('does not infer the history tab from a partial selection pair', async () => {
      setSearch('?psCommit=abc123');
      await renderOneStrategy();

      expect(tab('Overview').getAttribute('aria-selected')).toBe('true');
    });

    it('writes psView=history when switching to the history tab', async () => {
      setSearch('');
      await renderOneStrategy();
      const replaceState = vi.spyOn(window.history, 'replaceState');

      tab('History').dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      expect(lastSearchParams(replaceState).get('psView')).toBe('history');
    });

    it('removes psView when switching back to the card tab', async () => {
      setSearch('?psView=history');
      await renderOneStrategy();
      const replaceState = vi.spyOn(window.history, 'replaceState');

      tab('Overview').dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      expect(lastSearchParams(replaceState).get('psView')).toBeNull();
    });

    it('clears a lingering selection when switching back to the card tab', async () => {
      setSearch('?psView=history&psCommit=abc123&psEnv=main');
      await renderOneStrategy();
      const replaceState = vi.spyOn(window.history, 'replaceState');

      tab('Overview').dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      const params = lastSearchParams(replaceState);
      expect(params.get('psView')).toBeNull();
      expect(params.get('psCommit')).toBeNull();
      expect(params.get('psEnv')).toBeNull();
    });

    it('preserves unrelated ArgoCD params when writing psView', async () => {
      setSearch('?resource=&view=tree&node=argoproj.io%2FRollout');
      await renderOneStrategy();
      const replaceState = vi.spyOn(window.history, 'replaceState');

      tab('History').dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      const params = lastSearchParams(replaceState);
      expect(params.get('psView')).toBe('history');
      expect(params.get('view')).toBe('tree');
      expect(params.get('node')).toBe('argoproj.io/Rollout');
      expect(params.get('promotionstrategy')).toBe('default/only-strategy');
    });
  });

  describe('history view state deep linking', () => {
    const setSearch = (search: string) => {
      window.history.replaceState(null, '', '/applications/test-app' + search);
    };

    const renderHistory = async () => {
      vi.mocked(fetch).mockResolvedValue({
        ok: true,
        json: async () => ({ manifest: makeStrategyWithHistory('only-strategy') }),
        text: async () => '',
      } as Response);

      await render(makeProps([makeTreeNode('only-strategy')]));
    };

    const trigger = (label: string) =>
      container.querySelector(`.hp-dd__trigger[aria-label="${label}"]`) as HTMLButtonElement;

    const triggerValue = (label: string) =>
      trigger(label)?.querySelector('.hp-dd__value')?.textContent;

    const openMenu = async (label: string) => {
      trigger(label).dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();
    };

    const menuItem = (text: string) =>
      Array.from(document.querySelectorAll('#hp-dropdown-menu .hp-dd__item')).find((item) =>
        item.querySelector('.hp-dd__item-label')?.textContent?.includes(text),
      ) as HTMLButtonElement;

    const chooseItem = async (label: string, text: string) => {
      await openMenu(label);
      menuItem(text).dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();
    };

    const lastSearchParams = (replaceState: ReturnType<typeof vi.spyOn>) => {
      const calls = replaceState.mock.calls;
      const url = new URL(calls[calls.length - 1][2] as string, 'http://localhost');
      return url.searchParams;
    };

    afterEach(() => {
      setSearch('');
    });

    it('seeds the filter dropdown from psFilter', async () => {
      setSearch('?psView=history&psFilter=failed');
      await renderHistory();

      expect(triggerValue('Filter')).toBe('Failed');
    });

    it('seeds the sort dropdown from psSort', async () => {
      setSearch('?psView=history&psSort=oldest');
      await renderHistory();

      expect(triggerValue('Sort')).toBe('Oldest first');
    });

    it('seeds the environment dropdown from psEnvs', async () => {
      setSearch(
        `?psView=history&psEnvs=${encodeURIComponent(DEV_BRANCH)}&psEnvs=${encodeURIComponent(PRD_BRANCH)}`,
      );
      await renderHistory();

      expect(triggerValue('Environment')).toBe('2 environments');
    });

    it('falls back to the default filter for an unrecognized psFilter', async () => {
      setSearch('?psView=history&psFilter=bogus');
      await renderHistory();

      expect(triggerValue('Filter')).toBe('All commits');
    });

    it('shows all defaults when no history params are present', async () => {
      setSearch('?psView=history');
      await renderHistory();

      expect(triggerValue('Filter')).toBe('All commits');
      expect(triggerValue('Sort')).toBe('Newest first');
      expect(triggerValue('Environment')).toBe('All environments');
    });

    it('writes psSort when changing the sort dropdown', async () => {
      setSearch('?psView=history');
      await renderHistory();
      const replaceState = vi.spyOn(window.history, 'replaceState');

      await chooseItem('Sort', 'Oldest first');

      expect(triggerValue('Sort')).toBe('Oldest first');
      expect(lastSearchParams(replaceState).get('psSort')).toBe('oldest');
    });

    it('removes psSort when changing the sort back to the default', async () => {
      setSearch('?psView=history&psSort=oldest');
      await renderHistory();
      const replaceState = vi.spyOn(window.history, 'replaceState');

      await chooseItem('Sort', 'Newest first');

      expect(triggerValue('Sort')).toBe('Newest first');
      expect(lastSearchParams(replaceState).get('psSort')).toBeNull();
    });

    it('preserves unrelated ArgoCD params when writing history view state', async () => {
      setSearch(
        '?resource=&node=argoproj.io%2FApplication%2Fargocd%2Ftest&promotionstrategy=default%2Fonly-strategy&psView=history',
      );
      await renderHistory();
      const replaceState = vi.spyOn(window.history, 'replaceState');

      await chooseItem('Filter', 'Failed');

      const params = lastSearchParams(replaceState);
      expect(params.get('psFilter')).toBe('failed');
      expect(params.get('resource')).toBe('');
      expect(params.get('node')).toBe('argoproj.io/Application/argocd/test');
      expect(params.get('promotionstrategy')).toBe('default/only-strategy');
      expect(params.get('psView')).toBe('history');
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
