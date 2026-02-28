import { expect } from 'chai';
import React from 'react';
import { createRoot } from 'react-dom/client';

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
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const App = require('../src/App').default;

      const root = createRoot(container);

      // Should not throw an error
      expect(() => {
        root.render(React.createElement(App));
      }).to.not.throw();

      // Clean up
      root.unmount();
    });

    it('should render the TopBar component', async () => {
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const App = require('../src/App').default;

      const root = createRoot(container);
      root.render(React.createElement(App));

      // Wait for render
      await new Promise(resolve => setTimeout(resolve, 100));

      // Check that something rendered
      expect(container.innerHTML).to.not.equal('');

      root.unmount();
    });
  });

  describe('DashboardPage Component', () => {
    it('should render the DashboardPage component without crashing', () => {
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const DashboardPage = require('../src/pages/DashboardPage').default;

      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(DashboardPage));
      }).to.not.throw();

      root.unmount();
    });
  });

  describe('PromotionStrategyPage Component', () => {
    it('should render the PromotionStrategyPage component without crashing', () => {
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const PromotionStrategyPage = require('../src/pages/PromotionStrategyPage').default;

      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(PromotionStrategyPage, { namespace: 'test-namespace' }));
      }).to.not.throw();

      root.unmount();
    });
  });

  describe('TopBar Component', () => {
    it('should render the TopBar component without crashing', () => {
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const { TopBar } = require('../src/components/TopBar');

      const root = createRoot(container);

      expect(() => {
        root.render(React.createElement(TopBar));
      }).to.not.throw();

      root.unmount();
    });
  });
});
