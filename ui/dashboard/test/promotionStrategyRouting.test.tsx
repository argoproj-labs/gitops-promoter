import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React, { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router';
import type { PromotionStrategy } from '@shared/utils/PSData';
import type { PromotionExplorerProps } from '@lib/components/PromotionExplorer/PromotionExplorer';

const strategy = {
  metadata: { name: 'my-ps', namespace: 'ns' },
  spec: { gitRepositoryRef: { name: 'repo' }, environments: [] },
  status: { environments: [] },
} as unknown as PromotionStrategy;

const explorerProps = vi.hoisted(() => ({ current: null as PromotionExplorerProps | null }));

vi.mock('@lib/components/PromotionExplorer/PromotionExplorer', () => ({
  default: (props: PromotionExplorerProps) => {
    explorerProps.current = props;
    return (
      <div data-testid="explorer">
        <button
          data-testid="select"
          onClick={() => props.onSelectionChange?.({ rowId: 'abc123', branch: 'env/prod' })}
        />
        <button data-testid="deselect" onClick={() => props.onSelectionChange?.(null)} />
        <button data-testid="pin" onClick={() => props.onSelectedEnvsChange?.(['env/staging'])} />
        <button
          data-testid="pin-many"
          onClick={() => props.onSelectedEnvsChange?.(['env/staging', 'env/prod'])}
        />
        <button data-testid="unpin" onClick={() => props.onSelectedEnvsChange?.([])} />
      </div>
    );
  },
}));

vi.mock('@lib/components/LiveManifestView', () => ({
  LiveManifestView: () => <div data-testid="manifest" />,
}));

vi.mock('../src/stores/PromotionStrategyStore', () => ({
  PromotionStrategyStore: () => ({
    items: [strategy],
    loading: false,
    error: null,
    fetchItems: vi.fn(),
    subscribe: vi.fn(),
    unsubscribe: vi.fn(),
  }),
}));

vi.mock('../src/stores/NamespaceStore', () => {
  const state = { namespace: 'ns', namespaces: ['ns'], setNamespace: vi.fn() };
  return { namespaceStore: (selector: (_s: typeof state) => unknown) => selector(state) };
});

const { AppRoutes } = await import('../src/App');
const { PromotionStrategiesTiles } =
  await import('../src/components/PromotionStrategySummary/PromotionStrategyTiles');
const { TopBar } = await import('../src/components/TopBar');

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const LocationProbe = () => {
  const { pathname, search } = useLocation();
  return <div data-testid="location">{pathname + search}</div>;
};

describe('PromotionStrategyPage routing', () => {
  let container: HTMLDivElement;
  let root: Root;

  const renderAt = async (url: string, extra?: React.ReactNode) => {
    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={[url]}>
          {extra}
          <AppRoutes />
          <LocationProbe />
        </MemoryRouter>,
      );
    });
  };

  const q = (testId: string) => container.querySelector(`[data-testid="${testId}"]`);
  const click = async (el: Element | null) => {
    await act(async () => {
      el!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
  };
  const location = () => new URL(q('location')!.textContent!, 'http://localhost');
  const buttonByText = (text: string) =>
    Array.from(container.querySelectorAll('button')).find((b) => b.textContent?.trim() === text) ??
    null;

  beforeEach(() => {
    explorerProps.current = null;
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it('renders the explorer as the primary page for a strategy', async () => {
    await renderAt('/promotion-strategies/ns/my-ps');

    expect(q('explorer')).not.toBeNull();
    expect(q('manifest')).toBeNull();
    expect(explorerProps.current).toMatchObject({
      name: 'my-ps',
      namespace: 'ns',
      initialSelection: null,
      initialSelectedEnvs: [],
    });
  });

  it('has no Overview or History toggle', async () => {
    await renderAt('/promotion-strategies/ns/my-ps');

    expect(container.textContent).not.toContain('Overview');
    expect(buttonByText('History')).toBeNull();
    expect(container.querySelector('.gp-view-toggle')).toBeNull();
    expect(buttonByText('Explorer')).not.toBeNull();
    expect(buttonByText('Live manifest')).not.toBeNull();
  });

  it.each([
    '/promotion-strategies/ns/my-ps/history',
    '/promotion-strategies/ns/my-ps/history/',
    '/promotion-strategies/ns/my-ps/history/anything/else',
  ])('redirects legacy path %s to the explorer route', async (legacy) => {
    await renderAt(legacy);

    expect(location().pathname).toBe('/promotion-strategies/ns/my-ps');
    expect(q('explorer')).not.toBeNull();
  });

  it('preserves the query string when redirecting a legacy history link', async () => {
    await renderAt('/promotion-strategies/ns/my-ps/history?commit=abc123&env=env%2Fprod&mock=true');

    expect(location().pathname).toBe('/promotion-strategies/ns/my-ps');
    expect(location().searchParams.get('commit')).toBe('abc123');
    expect(location().searchParams.get('env')).toBe('env/prod');
    expect(location().searchParams.get('mock')).toBe('true');
    expect(explorerProps.current).toMatchObject({
      initialSelection: { rowId: 'abc123', branch: 'env/prod' },
    });
  });

  it('restores selection and selected environments from the query string', async () => {
    await renderAt(
      '/promotion-strategies/ns/my-ps?commit=abc123&env=env%2Fprod&node=env%2Fstaging',
    );

    expect(explorerProps.current).toMatchObject({
      initialSelection: { rowId: 'abc123', branch: 'env/prod' },
      initialSelectedEnvs: ['env/staging'],
    });
  });

  it('restores several selected environments from a comma separated node param', async () => {
    await renderAt('/promotion-strategies/ns/my-ps?node=env%2Fstaging%2Cenv%2Fprod');

    expect(explorerProps.current).toMatchObject({
      initialSelectedEnvs: ['env/staging', 'env/prod'],
    });
  });

  it('ignores a commit without an env', async () => {
    await renderAt('/promotion-strategies/ns/my-ps?commit=abc123');

    expect(explorerProps.current).toMatchObject({ initialSelection: null });
  });

  it('writes selection and selected environments to the query string and clears them', async () => {
    await renderAt('/promotion-strategies/ns/my-ps?mock=true');

    await click(q('select'));
    expect(location().searchParams.get('commit')).toBe('abc123');
    expect(location().searchParams.get('env')).toBe('env/prod');

    await click(q('pin'));
    expect(location().searchParams.get('node')).toBe('env/staging');
    expect(location().searchParams.get('commit')).toBe('abc123');
    expect(location().searchParams.get('mock')).toBe('true');

    await click(q('pin-many'));
    expect(location().searchParams.get('node')).toBe('env/staging,env/prod');
    expect(location().searchParams.get('commit')).toBe('abc123');

    await click(q('unpin'));
    expect(location().searchParams.has('node')).toBe(false);
    expect(location().searchParams.get('commit')).toBe('abc123');

    await click(q('deselect'));
    expect(location().searchParams.has('commit')).toBe(false);
    expect(location().searchParams.has('env')).toBe(false);
    expect(location().searchParams.get('mock')).toBe('true');
  });

  it('switches to the live manifest via ?view=manifest and back', async () => {
    await renderAt('/promotion-strategies/ns/my-ps?commit=abc123&env=env%2Fprod');

    await click(buttonByText('Live manifest'));
    expect(location().searchParams.get('view')).toBe('manifest');
    expect(q('manifest')).not.toBeNull();
    expect(q('explorer')).toBeNull();

    await click(buttonByText('Explorer'));
    expect(location().searchParams.has('view')).toBe(false);
    expect(q('explorer')).not.toBeNull();
    expect(location().searchParams.get('commit')).toBe('abc123');
  });

  it('opens the live manifest directly from the query string', async () => {
    await renderAt('/promotion-strategies/ns/my-ps?view=manifest');

    expect(q('manifest')).not.toBeNull();
    expect(q('explorer')).toBeNull();
  });

  it('goes back to the list without carrying explorer state', async () => {
    await renderAt('/promotion-strategies/ns/my-ps?mock=true&commit=abc123&env=env%2Fprod&node=x');

    await click(container.querySelector('[aria-label="Back"]'));

    expect(location().pathname).toBe('/promotion-strategies');
    expect(location().search).toBe('?mock=true');
  });

  it('shows a message for an unknown strategy', async () => {
    await renderAt('/promotion-strategies/ns/missing');

    expect(q('explorer')).toBeNull();
    expect(container.textContent).toContain("couldn't find a promotion strategy named missing");
  });

  it('links tiles to the explorer route without stale explorer state', async () => {
    await renderAt(
      '/promotion-strategies?mock=true&commit=old&env=old',
      <PromotionStrategiesTiles promotionStrategies={[strategy]} namespace="ns" />,
    );

    await click(container.querySelector('.ps-tile'));

    expect(location().pathname).toBe('/promotion-strategies/ns/my-ps');
    expect(location().search).toBe('?mock=true');
    expect(q('explorer')).not.toBeNull();
  });

  it('keeps the top bar link free of explorer state', async () => {
    await renderAt(
      '/promotion-strategies/ns/my-ps?mock=true&commit=abc123&env=e&node=n',
      <TopBar />,
    );

    const brand = container.querySelector('a.topbar__brand') as HTMLAnchorElement;
    expect(brand.getAttribute('href')).toBe('/promotion-strategies?mock=true');
  });
});
