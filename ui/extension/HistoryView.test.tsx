/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';
import HistoryView from '@components-lib/components/HistoryView/HistoryView';
import type {
  HistoryUrlState,
  CellSelection,
} from '@components-lib/components/HistoryView/HistoryView';
import type { PromotionStrategy } from '@shared/types/promotion';

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

// Row id is derived from the first 7 chars of a commit sha (see helpers.ts commitKey).
const NEWER_ROW_ID = 'aaaaaaa';

const makeStrategy = (): PromotionStrategy =>
  ({
    kind: 'PromotionStrategy',
    apiVersion: 'promoter.argoproj.io/v1alpha1',
    metadata: { name: 'my-strategy', namespace: 'default' },
    spec: {
      gitRepositoryRef: { name: 'my-repo' },
      environments: [{ branch: DEV_BRANCH }, { branch: PRD_BRANCH }],
    },
    status: {
      environments: [
        {
          branch: DEV_BRANCH,
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
          ],
        },
        {
          branch: PRD_BRANCH,
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
        },
      ],
    },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any;

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
Element.prototype.scrollIntoView = vi.fn();

describe('HistoryView', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
  });

  afterEach(() => {
    root?.unmount();
    container.remove();
    vi.restoreAllMocks();
  });

  const wait = (ms = 20) => new Promise((resolve) => setTimeout(resolve, ms));

  const render = async (props: {
    initialSelection?: CellSelection | null;
    initialViewState?: { filter?: string; sort?: string; envFilter?: string[] };
    onUrlStateChange?: (state: HistoryUrlState) => void;
  }) => {
    root = createRoot(container);
    root.render(
      React.createElement(HistoryView, {
        strategy: makeStrategy(),
        ...props,
      } as React.ComponentProps<typeof HistoryView>),
    );
    await wait();
  };

  const trigger = (label: string) =>
    container.querySelector(`.hp-dd__trigger[aria-label="${label}"]`) as HTMLButtonElement;

  const openMenu = async (label: string) => {
    trigger(label).dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    await wait();
  };

  const menuItem = (text: string) =>
    Array.from(document.querySelectorAll('#hp-dropdown-menu .hp-dd__item')).find((item) =>
      item.querySelector('.hp-dd__item-label')?.textContent?.includes(text),
    ) as HTMLButtonElement;

  const toggleEnvFilter = async (branch: string) => {
    await openMenu('Environment');
    menuItem(branch).dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    await wait();
  };

  const cellFor = (rowId: string, branch: string) =>
    container.querySelector(`#row-${rowId}`)?.querySelectorAll('.cell')[
      branch === DEV_BRANCH ? 0 : 1
    ] as HTMLElement | undefined;

  describe('selecting a cell then filtering out its environment', () => {
    it('clears a link-seeded selection once its environment is filtered out', async () => {
      await render({ initialSelection: { rowId: NEWER_ROW_ID, branch: DEV_BRANCH } });

      // Seeded from a link: the selection should render as selected initially.
      expect(container.querySelector('.hp-row--selected')).not.toBeNull();

      await toggleEnvFilter(PRD_BRANCH);

      expect(container.querySelector('.hp-row--selected')).toBeNull();
    });

    it('clears an interactively-made selection once its environment is filtered out', async () => {
      await render({});

      const cell = cellFor(NEWER_ROW_ID, DEV_BRANCH);
      cell?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await wait();

      expect(container.querySelector('.hp-row--selected')).not.toBeNull();

      await toggleEnvFilter(PRD_BRANCH);

      expect(container.querySelector('.hp-row--selected')).toBeNull();
    });
  });

  describe('URL-seeded selection validity', () => {
    it('keeps a selection whose environment matches the current data', async () => {
      await render({ initialSelection: { rowId: NEWER_ROW_ID, branch: DEV_BRANCH } });

      expect(container.querySelector('.hp-row--selected')).not.toBeNull();
      expect(container.querySelector('.hp-stale-link')).toBeNull();
    });

    it('flags an unknown branch in the URL selection as stale', async () => {
      await render({ initialSelection: { rowId: NEWER_ROW_ID, branch: 'environments/gone' } });

      expect(container.querySelector('.hp-row--selected')).toBeNull();
      expect(container.querySelector('.hp-stale-link')).not.toBeNull();
    });

    it('flags an unknown row id in the URL selection as stale', async () => {
      await render({ initialSelection: { rowId: 'ffffff0', branch: DEV_BRANCH } });

      expect(container.querySelector('.hp-row--selected')).toBeNull();
      expect(container.querySelector('.hp-stale-link')).not.toBeNull();
    });
  });

  describe('onUrlStateChange reset behavior (sameUrlState)', () => {
    it('does not re-dispatch when re-rendered with an equivalent envFilter in a different order', async () => {
      const onUrlStateChange = vi.fn();
      await render({
        initialViewState: { envFilter: [DEV_BRANCH, PRD_BRANCH] },
        onUrlStateChange,
      });
      onUrlStateChange.mockClear();

      root.render(
        React.createElement(HistoryView, {
          strategy: makeStrategy(),
          initialViewState: { envFilter: [DEV_BRANCH, PRD_BRANCH] },
          onUrlStateChange,
        } as React.ComponentProps<typeof HistoryView>),
      );
      await wait();

      expect(onUrlStateChange).not.toHaveBeenCalled();
    });

    it('treats a changed envFilter order as a distinct state and resets to it', async () => {
      const onUrlStateChange = vi.fn();
      await render({
        initialViewState: { envFilter: [DEV_BRANCH, PRD_BRANCH] },
        onUrlStateChange,
      });

      root.render(
        React.createElement(HistoryView, {
          strategy: makeStrategy(),
          initialViewState: { envFilter: [PRD_BRANCH, DEV_BRANCH] },
          onUrlStateChange,
        } as React.ComponentProps<typeof HistoryView>),
      );
      await wait();

      // Environment dropdown reflects the newly-applied (reset) filter, not the stale one.
      expect(trigger('Environment').querySelector('.hp-dd__value')?.textContent).toContain(
        '2 environments',
      );
    });
  });
});
