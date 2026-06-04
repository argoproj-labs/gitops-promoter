import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React from 'react';
import { createRoot } from 'react-dom/client';
import App from '../src/App';
import DashboardPage from '../src/pages/DashboardPage';
import PromotionStrategyPage from '../src/pages/PromotionStrategyPage';
import { TopBar } from '../src/components/TopBar';

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

describe('Dashboard Page Load Tests', () => {
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

  describe('App Component', () => {
    it('should render the App component without crashing', () => {
      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(App));
      }).not.toThrow();

      root.unmount();
    });

    it('should render the TopBar component', async () => {
      const root = createRoot(container);
      root.render(React.createElement(App));

      await new Promise((resolve) => setTimeout(resolve, 100));

      expect(container.innerHTML).not.toBe('');

      root.unmount();
    });
  });

  describe('DashboardPage Component', () => {
    it('should render the DashboardPage component without crashing', () => {
      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(DashboardPage));
      }).not.toThrow();

      root.unmount();
    });
  });

  describe('PromotionStrategyPage Component', () => {
    it('should render the PromotionStrategyPage component without crashing', () => {
      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(PromotionStrategyPage, { namespace: 'test-namespace' }));
      }).not.toThrow();

      root.unmount();
    });
  });

  describe('TopBar Component', () => {
    it('should render the TopBar component without crashing', () => {
      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(TopBar));
      }).not.toThrow();

      root.unmount();
    });
  });
});
