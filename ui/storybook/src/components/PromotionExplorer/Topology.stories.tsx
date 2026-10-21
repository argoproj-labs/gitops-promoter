import type { Meta, StoryObj } from '@storybook/react-vite';
import PromotionExplorer from '@lib/components/PromotionExplorer/PromotionExplorer';
import type { PromotionExplorerProps } from '@lib/components/PromotionExplorer/PromotionExplorer';
import {
  DAG_COMMIT,
  DAG_E2E_US,
  DAG_PROD_EU,
  dagStrategy,
  linearStrategy,
} from './topologyFixtures';

const meta: Meta<PromotionExplorerProps> = {
  title: 'Promotion Explorer/Topology',
  component: PromotionExplorer,
  parameters: { layout: 'fullscreen' },
  render: (args) => (
    <div style={{ height: '100vh' }}>
      <PromotionExplorer {...args} fillViewport />
    </div>
  ),
};

export default meta;

type Story = StoryObj<PromotionExplorerProps>;

/** A linear strategy renders the promotion map as a simple left-to-right chain. */
export const LinearChain: Story = {
  args: { strategy: linearStrategy, namespace: 'default', defaultMapExpanded: true },
};

/** The default state: a thin strip with a thumbnail and an "Open promotion map" button. */
export const CollapsedMap: Story = {
  args: { strategy: dagStrategy, namespace: 'default' },
};

/** dev fans out to three e2e environments, which fan back in to prod-eu. */
export const SevenEnvironmentDag: Story = {
  args: { strategy: dagStrategy, namespace: 'default', defaultMapExpanded: true },
};

/** A selected node filters the history grid to that environment. */
export const SelectedNode: Story = {
  args: {
    strategy: dagStrategy,
    namespace: 'default',
    defaultMapExpanded: true,
    initialSelectedEnvs: [DAG_PROD_EU],
  },
};

/** Shift+click adds environments to the selection; the grid shows only those columns. */
export const MultipleSelectedNodes: Story = {
  args: {
    strategy: dagStrategy,
    namespace: 'default',
    defaultMapExpanded: true,
    initialSelectedEnvs: [DAG_E2E_US, DAG_PROD_EU],
  },
};

/** A selected cell opens the detail drawer and lights that commit's path through the map. */
export const SelectedCommit: Story = {
  args: {
    strategy: dagStrategy,
    namespace: 'default',
    defaultMapExpanded: true,
    initialSelection: { rowId: DAG_COMMIT, branch: DAG_E2E_US },
  },
};
