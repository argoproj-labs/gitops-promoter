import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PromotionExplorer from '@lib/components/PromotionExplorer/PromotionExplorer';
import type { Environment } from '@shared/types/promotion';
import type { PromotionExplorerProps } from '@lib/components/PromotionExplorer/PromotionExplorer';
import { dagStrategy, linearStrategy, makeStrategy, rowId } from './fixtures';

const B = (name: string) => `environments/${name}`;

type Rendered = ReturnType<typeof render>;

function mount(props: Partial<PromotionExplorerProps> = {}) {
  const user = userEvent.setup();
  const view = render(
    <PromotionExplorer
      strategy={dagStrategy()}
      name="demo"
      namespace="default"
      defaultMapExpanded
      {...props}
    />,
  );
  return Object.assign(view, { user });
}

const nodeEl = (view: Rendered, name: string) =>
  view.container.querySelector<HTMLElement>(`[data-node-id="${B(name)}"]`)!;
const edgeEl = (view: Rendered, from: string, to: string) =>
  view.container.querySelector(`[data-edge-id="${B(from)}->${B(to)}"]`)!;
const rowEl = (view: Rendered, c: string) =>
  view.container.querySelector<HTMLElement>(`#row-${rowId(c)}`)!;
const rowOrder = (view: Rendered) =>
  Array.from(view.container.querySelectorAll('.hp-row')).map((el) => el.id.replace('row-', ''));
const headPills = (view: Rendered) =>
  Array.from(view.container.querySelectorAll('.hp-matrix__head-pill')).map((el) => el.textContent);
const region = () => screen.getByRole('region', { name: 'Promotion map' });
const badge = (el: HTMLElement) => el.querySelector('.hp-status')!.textContent;

function stubMapHeight(height: number) {
  Object.defineProperty(region(), 'offsetHeight', { configurable: true, value: height });
}

describe('loading and empty states', () => {
  it('shows a loading message without a strategy', () => {
    render(<PromotionExplorer />);
    expect(screen.getByText(/Loading promotion history/)).toBeInTheDocument();
  });

  it('shows an empty state for a strategy with no environment history', () => {
    const onBack = vi.fn();
    render(
      <PromotionExplorer
        strategy={makeStrategy([{ branch: 'environments/dev' }])}
        name="demo"
        onBack={onBack}
      />,
    );
    expect(screen.getByText('No promotion history yet')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Back to demo' }));
    expect(onBack).toHaveBeenCalled();
  });

  it('renders a back button in the header', async () => {
    const onBack = vi.fn();
    const { user } = mount({ onBack });
    await user.click(screen.getByRole('button', { name: /Back to demo/ }));
    expect(onBack).toHaveBeenCalled();
  });
});

describe('promotion map for a linear strategy', () => {
  it('renders a simple chain with derived statuses', () => {
    const view = mount({ strategy: linearStrategy() });
    expect(view.container.querySelectorAll('.hp-node')).toHaveLength(3);
    expect(view.container.querySelectorAll('[data-edge-id]')).toHaveLength(2);
    expect(badge(nodeEl(view, 'dev'))).toBe('Live');
    expect(badge(nodeEl(view, 'staging'))).toBe('Proposed');
    expect(badge(nodeEl(view, 'prod'))).toBe('Live');
    expect(nodeEl(view, 'staging')).toHaveTextContent('4444444');
    expect(nodeEl(view, 'prod')).toHaveTextContent('2222222');
  });

  it('keeps grid columns in chain order', () => {
    const view = mount({ strategy: linearStrategy() });
    expect(headPills(view)).toEqual([
      'environments/dev',
      'environments/staging',
      'environments/prod',
    ]);
  });

  it('has no status legend or arrow decorations in the grid header', () => {
    const view = mount({ strategy: linearStrategy() });
    expect(screen.queryByRole('group', { name: 'Status legend' })).not.toBeInTheDocument();
    expect(view.container.querySelector('.hp-map__legend')).not.toBeInTheDocument();
    expect(view.container.querySelector('.hp-matrix__head svg')).not.toBeInTheDocument();
  });

  it('labels the commit column headers', () => {
    mount({ strategy: linearStrategy() });
    expect(screen.getByText('Dry commit')).toBeInTheDocument();
    expect(screen.getByText('Promotes to')).toBeInTheDocument();
  });
});

describe('promotion map for a DAG strategy', () => {
  it('renders all seven nodes and eight edges', () => {
    const view = mount();
    expect(view.container.querySelectorAll('.hp-node')).toHaveLength(7);
    expect(view.container.querySelectorAll('[data-edge-id]')).toHaveLength(8);
    expect(edgeEl(view, 'e2e-apac', 'prod-eu')).toBeInTheDocument();
  });

  it('derives each node status from the strategy', () => {
    const view = mount();
    expect(badge(nodeEl(view, 'dev'))).toBe('Live');
    expect(badge(nodeEl(view, 'e2e-us'))).toBe('Proposed');
    expect(badge(nodeEl(view, 'e2e-eu'))).toBe('Proposed');
    expect(badge(nodeEl(view, 'e2e-apac'))).toBe('Not reached');
    expect(badge(nodeEl(view, 'prod-us'))).toBe('Live');
    expect(badge(nodeEl(view, 'prod-eu'))).toBe('Failed');
    expect(badge(nodeEl(view, 'prod-apac'))).toBe('Not reached');
    expect(nodeEl(view, 'prod-eu')).toHaveClass('hp-node--failed');
    expect(nodeEl(view, 'e2e-apac')).toHaveClass('hp-node--not-reached');
  });

  it('only renders grid columns for environments with history, in graph order', () => {
    const view = mount();
    expect(headPills(view)).toEqual([
      B('dev'),
      B('e2e-us'),
      B('e2e-eu'),
      B('prod-us'),
      B('prod-eu'),
    ]);
  });

  it('sizes the expanded map to the graph', () => {
    mount();
    expect(region()).toHaveStyle({ height: '392px' });
  });
});

describe('collapse and expand', () => {
  it('starts collapsed as a strip with a thumbnail and an open button', () => {
    const view = mount({ defaultMapExpanded: false });
    expect(region()).toHaveClass('hp-map--collapsed');
    const toggle = screen.getByRole('button', { name: 'Open promotion map' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(within(toggle).getByTestId('hp-minigraph')).toBeInTheDocument();
    expect(view.container.querySelectorAll('.hp-minigraph__node')).toHaveLength(7);
    expect(screen.getByTestId('hp-map-graph')).toHaveAttribute('aria-hidden', 'true');
  });

  it('expands and collapses with the toggle', async () => {
    const { user } = mount({ defaultMapExpanded: false });
    await user.click(screen.getByRole('button', { name: 'Open promotion map' }));
    expect(region()).toHaveClass('hp-map--expanded');
    expect(region()).toHaveClass('hp-map--animating');
    expect(region()).toHaveStyle({ height: '392px' });
    expect(screen.getByTestId('hp-map-graph')).toHaveAttribute('aria-hidden', 'false');
    const toggle = screen.getByRole('button', { name: 'Collapse promotion map' });
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.queryByTestId('hp-minigraph')).not.toBeInTheDocument();

    await user.click(toggle);
    expect(region()).toHaveClass('hp-map--collapsed');
    expect(region()).toHaveStyle({ height: '80px' });
    expect(screen.getByRole('button', { name: 'Open promotion map' })).toBeInTheDocument();
  });

  it('releases the fixed height when the collapse transition ends', async () => {
    const { user } = mount();
    await user.click(screen.getByRole('button', { name: 'Collapse promotion map' }));
    const event = new Event('transitionend', { bubbles: true });
    Object.assign(event, { propertyName: 'height' });
    fireEvent(region(), event);
    expect(region()).not.toHaveClass('hp-map--animating');
    expect(region().style.height).toBe('');
  });

  it('refits the graph when expanding', async () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
    const height = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(400);
    try {
      const { user } = mount({ strategy: linearStrategy(), defaultMapExpanded: false });
      expect(screen.getByText('142%')).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Zoom out' }));
      expect(screen.getByText('132%')).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Open promotion map' }));
      expect(screen.getByText('117%')).toBeInTheDocument();
    } finally {
      width.mockRestore();
      height.mockRestore();
    }
  });
});

describe('zoom', () => {
  it('has in, out, fit and reset buttons', async () => {
    const { user } = mount();
    const zoom = screen.getByRole('group', { name: 'Zoom controls' });
    const readout = () => zoom.querySelector('.hp-zoom__value')!.textContent;
    expect(readout()).toBe('50%');
    await user.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(readout()).toBe('60%');
    await user.click(screen.getByRole('button', { name: 'Reset view' }));
    expect(readout()).toBe('100%');
    await user.click(screen.getByRole('button', { name: 'Zoom out' }));
    expect(readout()).toBe('90%');
    await user.click(screen.getByRole('button', { name: 'Fit to view' }));
    expect(readout()).toBe('50%');
  });

  it('clamps between 50% and 150%', async () => {
    const { user } = mount();
    const zoomIn = screen.getByRole('button', { name: 'Zoom in' });
    for (let i = 0; i < 14; i++) await user.click(zoomIn);
    expect(screen.getByText('150%')).toBeInTheDocument();
    const zoomOut = screen.getByRole('button', { name: 'Zoom out' });
    for (let i = 0; i < 14; i++) await user.click(zoomOut);
    expect(screen.getByText('50%')).toBeInTheDocument();
  });

  it('zooms with the mouse wheel while expanded and applies the transform', () => {
    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Reset view' }));
    const graph = screen.getByTestId('hp-map-graph');
    fireEvent.wheel(region(), { deltaY: -100 });
    expect(screen.getByText('105%')).toBeInTheDocument();
    expect(graph.style.transform).toContain('scale(1.05)');
  });

  it('leaves the wheel alone while collapsed', () => {
    mount({ defaultMapExpanded: false });
    fireEvent.click(screen.getByRole('button', { name: 'Reset view' }));
    fireEvent.wheel(region(), { deltaY: -100 });
    expect(screen.getByText('100%')).toBeInTheDocument();
  });
});

describe('pan', () => {
  it('pans after a drag past the threshold and suppresses the following click', () => {
    const view = mount();
    fireEvent.click(screen.getByRole('button', { name: 'Reset view' }));
    const dev = nodeEl(view, 'dev');
    const graph = screen.getByTestId('hp-map-graph');

    fireEvent.pointerDown(region(), { clientX: 100, clientY: 100, pointerId: 1, button: 0 });
    fireEvent.pointerMove(region(), { clientX: 102, clientY: 101, pointerId: 1 });
    expect(region()).not.toHaveClass('hp-map--panning');
    fireEvent.pointerMove(region(), { clientX: 160, clientY: 130, pointerId: 1 });
    expect(region()).toHaveClass('hp-map--panning');
    expect(graph.style.transform).toContain('translate(60px, 30px)');
    fireEvent.pointerUp(region(), { clientX: 160, clientY: 130, pointerId: 1 });
    expect(region()).not.toHaveClass('hp-map--panning');

    fireEvent.click(dev);
    expect(dev).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(dev);
    expect(dev).toHaveAttribute('aria-pressed', 'true');
  });

  it('does not start a pan from a node', () => {
    const view = mount();
    fireEvent.click(screen.getByRole('button', { name: 'Reset view' }));
    fireEvent.pointerDown(nodeEl(view, 'dev'), {
      clientX: 10,
      clientY: 10,
      pointerId: 1,
      button: 0,
    });
    fireEvent.pointerMove(region(), { clientX: 100, clientY: 100, pointerId: 1 });
    expect(region()).not.toHaveClass('hp-map--panning');
  });
});

describe('hover on the map', () => {
  it('does not highlight or dim anything when a node is hovered', async () => {
    const view = mount();
    await userEvent.hover(nodeEl(view, 'prod-eu'));
    expect(view.container.querySelectorAll('.hp-node--dimmed')).toHaveLength(0);
    expect(view.container.querySelectorAll('.hp-node--hovered')).toHaveLength(0);
    expect(view.container.querySelectorAll('.hp-edge--lit, .hp-edge--dimmed')).toHaveLength(0);
    expect(region()).not.toHaveClass('hp-map--inspecting');
  });

  it('does not highlight on keyboard focus', () => {
    const view = mount();
    nodeEl(view, 'prod-us').focus();
    expect(view.container.querySelectorAll('.hp-node--dimmed')).toHaveLength(0);
  });

  it('has no edge hover targets', () => {
    const view = mount();
    expect(view.container.querySelector('[data-edge-hit]')).not.toBeInTheDocument();
  });

  it('does not reorder grid rows or change columns when a node is hovered', async () => {
    const view = mount();
    const rowsBefore = rowOrder(view);
    const pillsBefore = headPills(view);
    await userEvent.hover(nodeEl(view, 'prod-us'));
    expect(rowOrder(view)).toEqual(rowsBefore);
    expect(headPills(view)).toEqual(pillsBefore);
    await userEvent.unhover(nodeEl(view, 'prod-us'));
    expect(rowOrder(view)).toEqual(rowsBefore);
  });

  it('does not reorder or add columns for a hovered node outside the env filter', async () => {
    const view = mount({ initialSelectedEnvs: [B('dev')] });
    await view.user.hover(nodeEl(view, 'prod-us'));
    expect(headPills(view)).toEqual([B('dev')]);
    expect(view.container.querySelector('.hp-matrix__head-env--extra')).not.toBeInTheDocument();
  });

  it('does not dim or relate grid rows when a row is hovered', async () => {
    const view = mount();
    const before = rowOrder(view);
    await userEvent.hover(rowEl(view, '4'));
    expect(view.container.querySelectorAll('.hp-row--dimmed, .hp-row--active')).toHaveLength(0);
    expect(view.container.querySelectorAll('.hp-node--path, .hp-node--dimmed')).toHaveLength(0);
    expect(rowOrder(view)).toEqual(before);
  });
});

describe('selecting environments on the map', () => {
  const selectedBranches = (view: Rendered) =>
    Array.from(view.container.querySelectorAll('.hp-node--pinned')).map((el) =>
      el.getAttribute('data-node-id'),
    );

  it('selects a node on click and filters the grid to its environment', async () => {
    const view = mount();
    const prodUs = nodeEl(view, 'prod-us');
    expect(prodUs).toHaveAttribute('aria-pressed', 'false');
    expect(prodUs).toHaveAccessibleName(`Select ${B('prod-us')}`);
    await view.user.click(prodUs);
    expect(prodUs).toHaveAttribute('aria-pressed', 'true');
    expect(prodUs).toHaveAccessibleName(`Deselect ${B('prod-us')}`);
    expect(prodUs).toHaveClass('hp-node--pinned');
    expect(headPills(view)).toEqual([B('prod-us')]);
    expect(rowOrder(view)).toEqual(['2222222', '1111111']);
    expect(screen.getByText('Showing 1 of 5 environments')).toBeInTheDocument();
  });

  it('never opens a node detail box', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'prod-us'));
    expect(screen.queryByRole('complementary', { name: /details/ })).not.toBeInTheDocument();
    expect(view.container.querySelector('.hp-inspector')).not.toBeInTheDocument();
  });

  it('keeps the environment dropdown in sync with the selection', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'prod-us'));
    const trigger = screen.getByRole('button', { name: 'Environment' });
    expect(trigger).toHaveTextContent(B('prod-us'));
    await view.user.click(trigger);
    expect(screen.getByRole('option', { name: /prod-us/ })).toHaveAttribute(
      'aria-selected',
      'true',
    );
  });

  it('reflects dropdown changes on the map nodes', async () => {
    const view = mount();
    await view.user.click(screen.getByRole('button', { name: 'Environment' }));
    await view.user.click(screen.getByRole('option', { name: /environments\/dev/ }));
    await view.user.keyboard('{Escape}');
    expect(nodeEl(view, 'dev')).toHaveAttribute('aria-pressed', 'true');
    expect(selectedBranches(view)).toEqual([B('dev')]);
  });

  it('replaces the selection on a plain click of another node', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'dev'));
    await view.user.click(nodeEl(view, 'prod-us'));
    expect(selectedBranches(view)).toEqual([B('prod-us')]);
    expect(headPills(view)).toEqual([B('prod-us')]);
  });

  it('adds and removes environments with shift+click', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'dev'));
    await view.user.keyboard('{Shift>}');
    await view.user.click(nodeEl(view, 'prod-us'));
    await view.user.click(nodeEl(view, 'e2e-us'));
    await view.user.keyboard('{/Shift}');
    expect(selectedBranches(view).sort()).toEqual([B('dev'), B('e2e-us'), B('prod-us')].sort());
    expect(headPills(view)).toEqual([B('dev'), B('e2e-us'), B('prod-us')]);
    expect(screen.getByRole('button', { name: 'Environment' })).toHaveTextContent('3 environments');

    await view.user.keyboard('{Shift>}');
    await view.user.click(nodeEl(view, 'e2e-us'));
    await view.user.keyboard('{/Shift}');
    expect(selectedBranches(view).sort()).toEqual([B('dev'), B('prod-us')].sort());
  });

  it('deselects the sole selected node with a plain click', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'prod-us'));
    await view.user.click(nodeEl(view, 'prod-us'));
    expect(nodeEl(view, 'prod-us')).toHaveAttribute('aria-pressed', 'false');
    expect(headPills(view)).toHaveLength(5);
  });

  it('narrows a multi-selection to one node on a plain click', async () => {
    const view = mount({ initialSelectedEnvs: [B('dev'), B('prod-us')] });
    await view.user.click(nodeEl(view, 'prod-us'));
    expect(selectedBranches(view)).toEqual([B('prod-us')]);
  });

  it('ignores nodes that have no grid column', async () => {
    const onSelectedEnvsChange = vi.fn();
    const view = mount({ onSelectedEnvsChange });
    await view.user.click(nodeEl(view, 'prod-apac'));
    expect(onSelectedEnvsChange).not.toHaveBeenCalled();
    expect(headPills(view)).toHaveLength(5);
  });

  it('clears the selection with Escape', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'prod-us'));
    await view.user.keyboard('{Escape}');
    expect(selectedBranches(view)).toEqual([]);
    expect(headPills(view)).toHaveLength(5);
  });

  describe('Escape scoping', () => {
    const mountWithHost = (host: React.ReactNode) => {
      const user = userEvent.setup();
      const view = render(
        <>
          {host}
          <PromotionExplorer
            strategy={dagStrategy()}
            name="demo"
            namespace="default"
            defaultMapExpanded
          />
        </>,
      );
      return Object.assign(view, { user });
    };

    it('ignores Escape typed in an input outside the explorer', async () => {
      const view = mountWithHost(<input aria-label="host search" />);
      await view.user.click(nodeEl(view, 'prod-us'));
      await view.user.click(screen.getByLabelText('host search'));
      await view.user.keyboard('{Escape}');
      expect(selectedBranches(view)).toEqual([B('prod-us')]);
    });

    it('ignores Escape typed in an editable element inside the explorer', async () => {
      const view = mount();
      await view.user.click(nodeEl(view, 'prod-us'));
      const input = document.createElement('input');
      view.container.querySelector('.hp')!.appendChild(input);
      input.focus();
      await view.user.keyboard('{Escape}');
      expect(selectedBranches(view)).toEqual([B('prod-us')]);
    });

    it('ignores Escape while focus is inside another dialog outside the explorer', async () => {
      const view = mountWithHost(
        <div role="dialog" aria-label="host dialog">
          <button type="button">dialog action</button>
        </div>,
      );
      await view.user.click(nodeEl(view, 'prod-us'));
      screen.getByRole('button', { name: 'dialog action' }).focus();
      await view.user.keyboard('{Escape}');
      expect(selectedBranches(view)).toEqual([B('prod-us')]);
    });

    it('ignores Escape while a modal outside the explorer is open', async () => {
      const view = mountWithHost(<div aria-modal="true" />);
      await view.user.click(nodeEl(view, 'prod-us'));
      (document.activeElement as HTMLElement | null)?.blur();
      await view.user.keyboard('{Escape}');
      expect(selectedBranches(view)).toEqual([B('prod-us')]);
    });

    it('ignores Escape when focus is elsewhere in the host page', async () => {
      const view = mountWithHost(<button type="button">host button</button>);
      await view.user.click(nodeEl(view, 'prod-us'));
      screen.getByRole('button', { name: 'host button' }).focus();
      await view.user.keyboard('{Escape}');
      expect(selectedBranches(view)).toEqual([B('prod-us')]);
    });

    it('ignores Escape events that were already handled', async () => {
      const view = mount();
      await view.user.click(nodeEl(view, 'prod-us'));
      const event = new KeyboardEvent('keydown', {
        key: 'Escape',
        cancelable: true,
        bubbles: true,
      });
      event.preventDefault();
      document.body.dispatchEvent(event);
      expect(selectedBranches(view)).toEqual([B('prod-us')]);
    });

    it('clears the selection when focus is on the body', async () => {
      const view = mount();
      await view.user.click(nodeEl(view, 'prod-us'));
      (document.activeElement as HTMLElement | null)?.blur();
      expect(document.activeElement).toBe(document.body);
      await view.user.keyboard('{Escape}');
      expect(selectedBranches(view)).toEqual([]);
    });

    it('clears the selection when focus is inside the explorer', async () => {
      const view = mount();
      await view.user.click(nodeEl(view, 'prod-us'));
      nodeEl(view, 'prod-us').focus();
      await view.user.keyboard('{Escape}');
      expect(selectedBranches(view)).toEqual([]);
    });
  });

  it('clears the selection when the empty canvas is clicked', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'prod-us'));
    await view.user.click(region());
    expect(selectedBranches(view)).toEqual([]);
  });

  it('keeps the selection when clicking map chrome', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'prod-us'));
    await view.user.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(nodeEl(view, 'prod-us')).toHaveAttribute('aria-pressed', 'true');
  });

  it('clears the selection from the grid banner', async () => {
    const view = mount();
    await view.user.click(nodeEl(view, 'prod-us'));
    await view.user.click(screen.getByRole('button', { name: 'Clear filter' }));
    expect(selectedBranches(view)).toEqual([]);
  });

  it('reports every change to the host and accepts an initial selection', async () => {
    const onSelectedEnvsChange = vi.fn();
    const view = mount({ onSelectedEnvsChange, initialSelectedEnvs: [B('prod-eu')] });
    expect(nodeEl(view, 'prod-eu')).toHaveAttribute('aria-pressed', 'true');
    expect(headPills(view)).toEqual([B('prod-eu')]);
    await view.user.click(nodeEl(view, 'dev'));
    expect(onSelectedEnvsChange).toHaveBeenLastCalledWith([B('dev')]);
    await view.user.keyboard('{Shift>}');
    await view.user.click(nodeEl(view, 'prod-us'));
    await view.user.keyboard('{/Shift}');
    expect(onSelectedEnvsChange).toHaveBeenLastCalledWith([B('dev'), B('prod-us')]);
    await view.user.keyboard('{Escape}');
    expect(onSelectedEnvsChange).toHaveBeenLastCalledWith([]);
  });

  it('reports dropdown changes to the host', async () => {
    const onSelectedEnvsChange = vi.fn();
    const view = mount({ onSelectedEnvsChange });
    await view.user.click(screen.getByRole('button', { name: 'Environment' }));
    await view.user.click(screen.getByRole('option', { name: /prod-us/ }));
    expect(onSelectedEnvsChange).toHaveBeenLastCalledWith([B('prod-us')]);
  });

  it('drops initial environments that do not exist', () => {
    const onSelectedEnvsChange = vi.fn();
    const view = mount({
      onSelectedEnvsChange,
      initialSelectedEnvs: ['environments/ghost', B('dev')],
    });
    expect(onSelectedEnvsChange).toHaveBeenCalledWith([B('dev')]);
    expect(headPills(view)).toEqual([B('dev')]);
  });

  it('keeps cell selection and the detail drawer independent of the environment filter', async () => {
    const onSelectionChange = vi.fn();
    const view = mount({ onSelectionChange });
    await view.user.click(nodeEl(view, 'prod-us'));
    const cell = within(rowEl(view, '2')).getAllByRole('button', {
      name: /in environments\/prod-us/,
    })[0];
    await view.user.click(cell);
    expect(onSelectionChange).toHaveBeenCalledWith({ rowId: rowId('2'), branch: B('prod-us') });
    expect(document.querySelector('.hp-drawer--open')).toBeInTheDocument();
    expect(nodeEl(view, 'prod-us')).toHaveAttribute('aria-pressed', 'true');
  });
});

describe('split handle', () => {
  const separator = () => screen.getByRole('separator', { name: 'Resize promotion map' });

  it('is a focusable horizontal separator', () => {
    mount();
    expect(separator()).toHaveAttribute('aria-orientation', 'horizontal');
    expect(separator()).toHaveAttribute('tabindex', '0');
  });

  it('resizes by 24px with the arrow keys', async () => {
    const { user } = mount();
    stubMapHeight(300);
    separator().focus();
    await user.keyboard('{ArrowDown}');
    expect(region()).toHaveStyle({ height: '324px' });
    stubMapHeight(324);
    await user.keyboard('{ArrowUp}{ArrowUp}');
    expect(region()).toHaveStyle({ height: '300px' });
  });

  it('ignores other keys', async () => {
    const { user } = mount();
    stubMapHeight(300);
    separator().focus();
    await user.keyboard('{ArrowLeft}a');
    expect(region()).toHaveStyle({ height: '392px' });
  });

  it('collapses when shrunk below the threshold', async () => {
    const { user } = mount();
    stubMapHeight(150);
    separator().focus();
    await user.keyboard('{ArrowUp}');
    expect(region()).toHaveClass('hp-map--collapsed');
  });

  it('auto-expands when dragged past the threshold from collapsed', () => {
    mount({ defaultMapExpanded: false });
    stubMapHeight(80);
    fireEvent.pointerDown(separator(), { clientY: 100, pointerId: 1 });
    fireEvent.pointerMove(separator(), { clientY: 130, pointerId: 1 });
    expect(region()).toHaveClass('hp-map--collapsed');
    fireEvent.pointerMove(separator(), { clientY: 200, pointerId: 1 });
    expect(region()).toHaveClass('hp-map--expanded');
    expect(region()).toHaveStyle({ height: '180px' });
    fireEvent.pointerUp(separator(), { pointerId: 1 });
    fireEvent.pointerMove(separator(), { clientY: 400, pointerId: 1 });
    expect(region()).toHaveStyle({ height: '180px' });
  });

  describe('fit when opened by dragging', () => {
    let restore: Array<() => void> = [];

    beforeEach(() => {
      const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1000);
      const height = vi
        .spyOn(HTMLElement.prototype, 'clientHeight', 'get')
        .mockImplementation(function (this: HTMLElement) {
          return this.style.height ? parseInt(this.style.height, 10) : 800;
        });
      restore = [() => width.mockRestore(), () => height.mockRestore()];
    });

    afterEach(() => restore.forEach((fn) => fn()));

    const zoomLabel = () => screen.getByTestId('hp-map-graph').style.transform;

    it('fits to the same zoom as the open button instead of the mid-drag height', async () => {
      const { user, unmount } = mount({ strategy: linearStrategy(), defaultMapExpanded: false });
      await user.click(screen.getByRole('button', { name: 'Open promotion map' }));
      const viaButton = zoomLabel();
      expect(screen.getByText('117%')).toBeInTheDocument();
      unmount();

      mount({ strategy: linearStrategy(), defaultMapExpanded: false });
      stubMapHeight(80);
      fireEvent.pointerDown(separator(), { clientY: 100, pointerId: 1 });
      fireEvent.pointerMove(separator(), { clientY: 180, pointerId: 1 });
      expect(region()).toHaveClass('hp-map--expanded');
      expect(region()).toHaveStyle({ height: '160px' });
      expect(zoomLabel()).toBe(viaButton);
      expect(screen.getByText('117%')).toBeInTheDocument();
    });

    it('refits to the final height when the drag ends', () => {
      mount({ strategy: linearStrategy(), defaultMapExpanded: false });
      stubMapHeight(80);
      fireEvent.pointerDown(separator(), { clientY: 100, pointerId: 1 });
      fireEvent.pointerMove(separator(), { clientY: 180, pointerId: 1 });
      fireEvent.pointerMove(separator(), { clientY: 220, pointerId: 1 });
      expect(region()).toHaveStyle({ height: '200px' });
      expect(screen.getByText('117%')).toBeInTheDocument();
      fireEvent.pointerUp(separator(), { pointerId: 1 });
      expect(screen.getByText('74%')).toBeInTheDocument();
    });

    it('keeps a manual zoom when the drag ends', async () => {
      const { user } = mount({ strategy: linearStrategy(), defaultMapExpanded: false });
      stubMapHeight(80);
      fireEvent.pointerDown(separator(), { clientY: 100, pointerId: 1 });
      fireEvent.pointerMove(separator(), { clientY: 180, pointerId: 1 });
      await user.click(screen.getByRole('button', { name: 'Zoom out' }));
      expect(screen.getByText('107%')).toBeInTheDocument();
      fireEvent.pointerMove(separator(), { clientY: 380, pointerId: 1 });
      fireEvent.pointerUp(separator(), { pointerId: 1 });
      expect(screen.getByText('107%')).toBeInTheDocument();
    });

    it('does not refit when the pointer is released without a drag start', () => {
      mount({ strategy: linearStrategy() });
      fireEvent.pointerUp(separator(), { pointerId: 1 });
      expect(screen.getByText('117%')).toBeInTheDocument();
    });
  });

  it('follows the pointer while expanded and never exceeds the available space', () => {
    mount();
    stubMapHeight(300);
    fireEvent.pointerDown(separator(), { clientY: 100, pointerId: 1 });
    fireEvent.pointerMove(separator(), { clientY: 160, pointerId: 1 });
    expect(region()).toHaveStyle({ height: '360px' });
    fireEvent.pointerMove(separator(), { clientY: 5000, pointerId: 1 });
    expect(region()).toHaveStyle({ height: '608px' });
    fireEvent.pointerCancel(separator(), { pointerId: 1 });
    fireEvent.pointerMove(separator(), { clientY: 120, pointerId: 1 });
    expect(region()).toHaveStyle({ height: '608px' });
  });

  it('collapses on double click', async () => {
    const { user } = mount();
    await user.dblClick(separator());
    expect(region()).toHaveClass('hp-map--collapsed');
  });
});

describe('commit rows and the map', () => {
  it('has no trace button in the dry commit column', () => {
    const view = mount();
    expect(screen.queryByRole('button', { name: /Trace .* in promotion map/ })).toBeNull();
    expect(view.container.querySelector('.hp-row__trace')).toBeNull();
  });

  it('lights the commit path of the selected cell, tinted by state', () => {
    const view = mount({ initialSelection: { rowId: rowId('4'), branch: B('dev') } });
    expect(nodeEl(view, 'dev')).toHaveClass('hp-node--path', 'hp-node--tint-live');
    expect(nodeEl(view, 'e2e-us')).toHaveClass('hp-node--path', 'hp-node--tint-proposed');
    expect(nodeEl(view, 'e2e-eu')).toHaveClass('hp-node--path', 'hp-node--tint-proposed');
    expect(nodeEl(view, 'prod-us')).toHaveClass('hp-node--dimmed');
    expect(edgeEl(view, 'dev', 'e2e-us')).toHaveClass('hp-edge--lit');
    expect(edgeEl(view, 'e2e-us', 'prod-us')).toHaveClass('hp-edge--dimmed');
  });

  it('includes untouched ancestors of the commit path without a tint', () => {
    const view = mount({ initialSelection: { rowId: rowId('1'), branch: B('dev') } });
    expect(nodeEl(view, 'e2e-apac')).toHaveClass('hp-node--path');
    expect(nodeEl(view, 'e2e-apac').className).not.toMatch(/tint/);
    expect(nodeEl(view, 'prod-eu')).toHaveClass('hp-node--tint-failed');
  });

  it('does not select a cell when the commit column is clicked', async () => {
    const onSelectionChange = vi.fn();
    const view = mount({ onSelectionChange });
    await userEvent.click(within(rowEl(view, '3')).getByText('feat: audit log'));
    expect(onSelectionChange).not.toHaveBeenCalled();
    expect(view.container.querySelectorAll('.hp-node--path')).toHaveLength(0);
  });

  it('highlights the commit path for the selected cell', () => {
    const view = mount({ initialSelection: { rowId: rowId('2'), branch: B('prod-us') } });
    expect(nodeEl(view, 'prod-us')).toHaveClass('hp-node--path');
    expect(nodeEl(view, 'prod-apac')).toHaveClass('hp-node--dimmed');
  });
});

describe('filters and sort', () => {
  const open = async (user: ReturnType<typeof userEvent.setup>, name: string) => {
    await user.click(screen.getByRole('button', { name }));
    return screen.getByRole('listbox');
  };

  it('labels the default filter All dry commits', () => {
    mount();
    expect(screen.getByRole('button', { name: 'Filter' })).toHaveTextContent('All dry commits');
    expect(screen.queryByText('All commits')).not.toBeInTheDocument();
  });

  it('shows counts for every status filter', async () => {
    const { user } = mount();
    const list = await open(user, 'Filter');
    const count = (label: RegExp) =>
      within(within(list).getByRole('option', { name: label })).getByText(/^\d+$/).textContent;
    expect(count(/All dry commits/)).toBe('4');
    expect(count(/^Live/)).toBe('3');
    expect(count(/In flight/)).toBe('1');
    expect(count(/Failed/)).toBe('1');
    expect(count(/No-op/)).toBe('0');
  });

  it('filters commits by status', async () => {
    const view = mount();
    const { user } = view;
    const list = await open(user, 'Filter');
    await user.click(within(list).getByRole('option', { name: /In flight/ }));
    expect(rowOrder(view)).toEqual(['4444444']);
    expect(screen.getByRole('status')).toHaveTextContent('1 commit shown');

    const again = await open(user, 'Filter');
    await user.click(within(again).getByRole('option', { name: /Failed/ }));
    expect(rowOrder(view)).toEqual(['1111111']);
  });

  it('shows an empty message when no commits match', async () => {
    const { user } = mount();
    const list = await open(user, 'Filter');
    await user.click(within(list).getByRole('option', { name: /No-op/ }));
    expect(screen.getByText(/No commits match these filters/)).toBeInTheDocument();
  });

  it('lists environments with counts and filters rows and columns', async () => {
    const view = mount();
    const { user } = view;
    const list = await open(user, 'Environment');
    expect(within(list).getAllByRole('option')).toHaveLength(6);
    const prodUs = within(list).getByRole('option', { name: /prod-us/ });
    expect(within(prodUs).getByText('2')).toBeInTheDocument();
    await user.click(prodUs);
    await user.keyboard('{Escape}');
    expect(rowOrder(view)).toEqual(['2222222', '1111111']);
    expect(headPills(view)).toEqual([B('prod-us')]);
    expect(screen.getByText('Showing 1 of 5 environments')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Clear filter' }));
    expect(rowOrder(view)).toHaveLength(4);
  });

  it('sorts oldest first', async () => {
    const view = mount();
    const { user } = view;
    const list = await open(user, 'Sort');
    await user.click(within(list).getByRole('option', { name: /Oldest first/ }));
    expect(rowOrder(view)).toEqual(['1111111', '2222222', '3333333', '4444444']);
    const again = await open(user, 'Sort');
    await user.click(within(again).getByRole('option', { name: /Newest first/ }));
    expect(rowOrder(view)).toEqual(['4444444', '3333333', '2222222', '1111111']);
  });

  it('hides the environment dropdown for a single environment', () => {
    mount({
      strategy: makeStrategy([{ branch: 'environments/only', active: '2', history: ['2'] }]),
    });
    expect(screen.queryByRole('button', { name: 'Environment' })).not.toBeInTheDocument();
  });
});

describe('cell selection and the detail drawer', () => {
  it('selects a cell and reports it to the host', async () => {
    const onSelectionChange = vi.fn();
    const view = mount({ onSelectionChange });
    const cell = within(rowEl(view, '3')).getAllByRole('button', {
      name: /in environments\/e2e-us/,
    })[0];
    await userEvent.click(cell);
    expect(onSelectionChange).toHaveBeenCalledWith({ rowId: rowId('3'), branch: B('e2e-us') });
    expect(document.querySelector('.hp-drawer--open')).toBeInTheDocument();
    expect(rowEl(view, '3')).toHaveClass('hp-row--selected');
  });

  it('closes the drawer and clears the selection', async () => {
    const onSelectionChange = vi.fn();
    mount({
      onSelectionChange,
      initialSelection: { rowId: rowId('3'), branch: B('e2e-us') },
    });
    expect(document.querySelector('.hp-drawer--open')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Close details' }));
    expect(onSelectionChange).toHaveBeenLastCalledWith(null);
    expect(document.querySelector('.hp-drawer--open')).not.toBeInTheDocument();
  });

  it('drops an initial selection that points at an empty cell', () => {
    const onSelectionChange = vi.fn();
    mount({
      onSelectionChange,
      initialSelection: { rowId: rowId('4'), branch: B('prod-us') },
    });
    expect(onSelectionChange).toHaveBeenCalledWith(null);
  });

  it('offsets the map chrome while the drawer is open', () => {
    const view = mount({ initialSelection: { rowId: rowId('3'), branch: B('e2e-us') } });
    const body = view.container.querySelector<HTMLElement>('.hp-body')!;
    expect(body.style.getPropertyValue('--hp-drawer-offset')).toMatch(/^\d+px$/);
    expect(body.style.getPropertyValue('--hp-drawer-offset')).not.toBe('0px');
  });
});

describe('host layout', () => {
  it('clamps to the viewport when asked', () => {
    const view = mount({ fillViewport: true });
    expect(view.container.querySelector('.hp')).toHaveClass('hp--viewport');
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

beforeEach(() => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

describe('dry commit column app commits', () => {
  const refSha = 'abcdef0123456789abcdef0123456789abcdef01';
  const ref = (extra: Record<string, unknown> = {}) => ({
    commit: {
      sha: refSha,
      repoURL: 'https://github.com/example/ledger-service',
      subject: 'fix(ledger): prevent double-posting (#1234)',
      author: 'Priya Raman <priya.raman@example.com>',
      date: new Date(Date.now() - 2 * 3_600_000).toISOString(),
      ...extra,
    },
  });

  function strategyWithRefs(refsFor4: unknown[], refsFor3: unknown[] = []) {
    const strategy = linearStrategy();
    for (const env of strategy.status!.environments! as Environment[]) {
      const dries = [
        env.active?.dry,
        env.proposed?.dry,
        ...(env.history ?? []).map((h) => h.active?.dry),
      ];
      for (const d of dries) {
        if (d?.sha === '4'.repeat(40)) d.references = refsFor4 as never;
        if (d?.sha === '3'.repeat(40)) d.references = refsFor3 as never;
      }
    }
    return strategy;
  }

  const appGroup = (view: Rendered, c: string) =>
    within(rowEl(view, c)).queryByRole('group', { name: 'App commit' });

  const dryBlock = (view: Rendered, c: string) =>
    within(rowEl(view, c)).getByRole('group', { name: 'Dry commit' });

  const structure = (block: Element) => {
    const line = block.querySelector(':scope > .hp-commit__line');
    const meta = block.querySelector(':scope > .hp-commit__meta');
    return {
      line,
      meta,
      lineChildren: Array.from(line?.children ?? []).map((el) =>
        el.classList.contains('hp-commit__icon-wrap')
          ? 'icon'
          : el.classList.contains('hp-commit__sha')
            ? 'chip'
            : el.classList.contains('hp-commit__title')
              ? 'title'
              : el.tagName.toLowerCase(),
      ),
      metaChildren: Array.from(meta?.children ?? []).map((el) =>
        Array.from(el.classList)
          .find((c) => c.startsWith('hp-commit__'))
          ?.replace('hp-commit__', ''),
      ),
    };
  };

  it('lays out the dry and app commits as identical two-line blocks', () => {
    const view = mount({ strategy: strategyWithRefs([ref()]) });
    const dry = dryBlock(view, '4');
    const app = appGroup(view, '4')!.querySelector('.hp-commit')!;
    expect(dry).toHaveClass('hp-commit');
    expect(app).toHaveClass('hp-commit');
    expect(dry.className).toBe(app.className);
    expect(dry.querySelector('.hp-commit__icon')).toBeInTheDocument();
    expect(app.querySelector('.hp-commit__icon')).toBeInTheDocument();
    expect(dry.querySelector('.hp-commit__icon')!.getAttribute('class')).toBe(
      app.querySelector('.hp-commit__icon')!.getAttribute('class'),
    );
    expect(dry.querySelector('.hp-commit__sha')!.className).toBe(
      app.querySelector('.hp-commit__sha')!.className,
    );
    expect(dry.querySelector('svg')!.innerHTML).not.toBe(app.querySelector('svg')!.innerHTML);
    const d = structure(dry);
    const a = structure(app);
    expect(d.lineChildren).toEqual(['icon', 'chip', 'title']);
    expect(a.lineChildren).toEqual(['icon', 'chip', 'title']);
    expect(d.metaChildren).toEqual(['author', 'sep', 'time']);
    expect(a.metaChildren).toEqual(d.metaChildren);
    expect(dry.querySelector('.hp-row__subject')).toBeNull();
    expect(within(d.line as HTMLElement).getByTitle('Dry commit')).toBeInTheDocument();
    expect(within(a.line as HTMLElement).getByTitle('App commit')).toBeInTheDocument();
    expect(d.line!.querySelector('.hp-commit__title')).toHaveTextContent(/\S/);
    expect(a.line!.querySelector('.hp-commit__title')).toHaveAttribute(
      'title',
      'fix(ledger): prevent double-posting (#1234)',
    );
    expect(a.meta).toHaveTextContent('Priya Raman');
    expect(a.meta).toHaveTextContent(/2 hours ago/);
    expect(a.line).not.toHaveTextContent('Priya');
  });

  it('puts the dry subject on line 1 and author and time on line 2', () => {
    const view = mount({ strategy: strategyWithRefs([]) });
    const dry = dryBlock(view, '4');
    const { line, meta } = structure(dry);
    const title = line!.querySelector('.hp-commit__title') as HTMLElement;
    expect(title.textContent).toBeTruthy();
    expect(title).toHaveAttribute('title', title.textContent!);
    expect(within(line as HTMLElement).getByRole('link')).toHaveAttribute('target', '_blank');
    expect(meta!.querySelector('.hp-commit__author')).toBeInTheDocument();
    expect(meta!.querySelector('.hp-commit__time')).toBeInTheDocument();
    expect(meta!.querySelector('.hp-commit__sep')).toBeInTheDocument();
    expect(line).not.toContainElement(meta as HTMLElement);
  });

  it('labels the dry commit line and shows the app commit line with its details', () => {
    const view = mount({ strategy: strategyWithRefs([ref()]) });
    expect(dryBlock(view, '4')).toBeInTheDocument();
    const line = appGroup(view, '4')!;
    const link = within(line).getByRole('link', {
      name: 'App commit abcdef0 in ledger-service, opens in new tab',
    });
    expect(link).toHaveAttribute(
      'href',
      `https://github.com/example/ledger-service/commit/${refSha}`,
    );
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(link).toHaveTextContent('abcdef0');
    expect(line).not.toHaveTextContent('ledger-service');
    expect(link).toHaveAttribute(
      'title',
      `Repository: ledger-service\nCommit: ${refSha.slice(0, 7)}`,
    );
    expect(within(line).getByTitle('fix(ledger): prevent double-posting (#1234)')).toBeVisible();
    expect(line).toHaveTextContent('Priya Raman');
    expect(line).toHaveTextContent(/2 hours ago/);
    expect(within(line).queryByRole('button')).not.toBeInTheDocument();
  });

  it('does not select the row when the app commit link is clicked', () => {
    const onSelectionChange = vi.fn();
    const view = mount({ strategy: strategyWithRefs([ref()]), onSelectionChange });
    const link = within(appGroup(view, '4')!).getByRole('link');
    link.addEventListener('click', (e) => e.preventDefault());
    fireEvent.click(link);
    expect(onSelectionChange).not.toHaveBeenCalled();
  });

  it('reveals the additional app commits behind a +N more toggle without selecting the row', () => {
    const second = ref({
      sha: '1234567890abcdef1234567890abcdef12345678',
      subject: 'second change',
    });
    const third = ref({ sha: 'fedcba0987654321fedcba0987654321fedcba09', subject: 'third change' });
    const onSelectionChange = vi.fn();
    const view = mount({
      strategy: strategyWithRefs([ref(), second, third]),
      onSelectionChange,
    });
    const line = appGroup(view, '4')!;
    const toggle = within(line).getByRole('button', { name: '+2 more' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(line).not.toHaveTextContent('second change');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(line).toHaveTextContent('second change');
    expect(line).toHaveTextContent('third change');
    expect(within(line).getAllByRole('link')).toHaveLength(3);
    const blocks = Array.from(line.querySelectorAll(':scope > .hp-commit'));
    expect(blocks).toHaveLength(3);
    blocks.forEach((b, i) => {
      expect(structure(b).lineChildren).toEqual(
        i === 0 ? ['icon', 'chip', 'title', 'button'] : ['icon', 'chip', 'title'],
      );
      expect(structure(b).metaChildren).toEqual(['author', 'sep', 'time']);
    });
    expect(onSelectionChange).not.toHaveBeenCalled();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(line).not.toHaveTextContent('second change');
  });

  it('renders no app commit line for a dry commit without references', () => {
    const view = mount({ strategy: strategyWithRefs([ref()]) });
    expect(appGroup(view, '3')).not.toBeInTheDocument();
    expect(within(rowEl(view, '3')).getByRole('group', { name: 'Dry commit' })).toBeInTheDocument();
  });

  it('renders the sha as plain text when the commit url is not http(s)', () => {
    const view = mount({
      strategy: strategyWithRefs([ref({ repoURL: 'javascript:alert(1)//' })]),
    });
    const line = appGroup(view, '4')!;
    expect(within(line).queryByRole('link')).not.toBeInTheDocument();
    expect(line).toHaveTextContent('abcdef0');
  });

  it('omits author and age when the app commit lacks them', () => {
    const view = mount({
      strategy: strategyWithRefs([ref({ author: undefined, date: undefined })]),
    });
    const line = appGroup(view, '4')!;
    expect(line).toHaveTextContent('abcdef0');
    expect(line).not.toHaveTextContent('ledger-service');
    expect(line).not.toHaveTextContent('Priya');
    expect(line).not.toHaveTextContent('ago');
    expect(line).not.toHaveTextContent('·');
    const { lineChildren, meta } = structure(line.querySelector('.hp-commit')!);
    expect(lineChildren).toEqual(['icon', 'chip', 'title']);
    expect(meta).toBeNull();
  });

  it('omits only the missing half and its separator on line 2', () => {
    const noDate = mount({ strategy: strategyWithRefs([ref({ date: undefined })]) });
    const authorOnly = structure(appGroup(noDate, '4')!.querySelector('.hp-commit')!);
    expect(authorOnly.metaChildren).toEqual(['author']);
    noDate.unmount();
    const noAuthor = mount({ strategy: strategyWithRefs([ref({ author: undefined })]) });
    const timeOnly = structure(appGroup(noAuthor, '4')!.querySelector('.hp-commit')!);
    expect(timeOnly.metaChildren).toEqual(['time']);
  });

  it('shows the app subject placeholder when the reference has no subject', () => {
    const view = mount({ strategy: strategyWithRefs([ref({ subject: undefined })]) });
    expect(within(appGroup(view, '4')!).getByTitle('(no subject)')).toBeVisible();
  });
});
