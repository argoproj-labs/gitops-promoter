import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import React from 'react';
import FlowCell from '@lib/components/HistoryView/FlowCell/FlowCell';
import type { CellState, CommitRow } from '@lib/components/HistoryView/types';
import type { Commit, EnrichedBranchCommitStatus, PullRequest } from '@shared/types/promotion';
import '@lib/components/HistoryView/index.scss';

const BRANCH = 'environment/staging';
const REPO_URL = 'https://github.com/argoproj-labs/gitops-promoter';
const NOW = Date.now();
const MINUTE = 60 * 1000;
const ago = (minutes: number) => new Date(NOW - minutes * MINUTE).toISOString();

function commit(sha: string, subject: string, minutesAgo: number): Commit {
  return {
    sha,
    subject,
    author: 'Jane Doe <jane@example.com>',
    body: `${subject}.`,
    commitTime: ago(minutesAgo),
    repoURL: REPO_URL,
  };
}

function mergedPR(id: number, mergedMinutesAgo: number): PullRequest {
  return {
    id: String(id),
    url: `${REPO_URL}/pull/${id}`,
    state: 'merged',
    prCreationTime: ago(mergedMinutesAgo + 15),
    prMergeTime: ago(mergedMinutesAgo),
  };
}

const passing: EnrichedBranchCommitStatus[] = [
  { key: 'argocd-health', phase: 'success', description: 'Application is Healthy' },
  { key: 'e2e-tests', phase: 'success', description: 'All 120 tests passed' },
];

const pending: EnrichedBranchCommitStatus[] = [
  { key: 'argocd-health', phase: 'success', description: 'Application is Healthy' },
  { key: 'soak-timer', phase: 'pending', description: 'Waiting for the soak period to elapse' },
];

const failing = (description: string): EnrichedBranchCommitStatus[] => [
  { key: 'argocd-health', phase: 'success', description: 'Application is Healthy' },
  { key: 'e2e-tests', phase: 'failure', description },
];

const REPLACER_SHA = 'f00dfacef00dfacef00dfacef00dfacef00dface';
const REPLACER_AT = ago(40);

const replacerRow: CommitRow = {
  id: REPLACER_SHA.slice(0, 7),
  dryShaFull: REPLACER_SHA,
  dryShaShort: REPLACER_SHA.slice(0, 7),
  subject: 'Roll back connection pool change',
  author: 'Jane Doe',
  repoUrl: REPO_URL,
  freshestAt: Date.parse(REPLACER_AT),
  earliestAt: Date.parse(REPLACER_AT),
  cells: {},
  hasLive: true,
  hasInFlight: false,
  hasFailed: false,
  hasNoop: false,
};

const rowsById = new Map<string, CommitRow>([[replacerRow.id, replacerRow]]);

const CELLS = {
  live: {
    kind: 'live',
    commit: commit('a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2', 'Bump replicas to 3', 25),
    commitStatuses: passing,
    health: 'success',
    pullRequest: mergedPR(142, 25),
    at: ago(25),
  },
  'in-flight': {
    kind: 'in-flight',
    commit: commit('b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3', 'Add readiness probe', 10),
    commitStatuses: pending,
    health: 'pending',
    pullRequest: {
      id: '143',
      url: `${REPO_URL}/pull/143`,
      state: 'open',
      prCreationTime: ago(10),
    },
    at: ago(10),
  },
  proposed: {
    kind: 'in-flight',
    isProposed: true,
    commit: commit('c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4', 'Tune HPA thresholds', 6),
    commitStatuses: pending,
    health: 'pending',
    at: ago(6),
  },
  failed: {
    kind: 'failed',
    commit: commit('d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5', 'Enable new checkout flow', 60),
    commitStatuses: failing('3 of 120 tests failed: checkout-flow'),
    health: 'failure',
    pullRequest: mergedPR(141, 60),
    at: ago(60),
  },
  'was-failed': {
    kind: 'was-failed',
    commit: commit('e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6', 'Raise connection pool size', 130),
    commitStatuses: failing('Smoke test /healthz returned 503'),
    health: 'failure',
    pullRequest: mergedPR(139, 130),
    supersededById: replacerRow.id,
    liveDurationMs: 90 * MINUTE,
    replacedAt: REPLACER_AT,
    at: ago(130),
  },
  'was-here': {
    kind: 'was-here',
    commit: commit('f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1', 'Update base image', 175),
    commitStatuses: passing,
    health: 'success',
    pullRequest: mergedPR(138, 175),
    supersededById: replacerRow.id,
    liveDurationMs: 135 * MINUTE,
    replacedAt: REPLACER_AT,
    at: ago(175),
  },
  'no-op': {
    kind: 'no-op',
    commit: commit('0a1b2c3d4e5f0a1b2c3d4e5f0a1b2c3d4e5f0a1b', 'Docs-only change', 180),
    commitStatuses: passing,
    health: 'success',
    pullRequest: mergedPR(137, 180),
    noopNote: `Same dry SHA as the previous entry, so ${BRANCH} didn't change.`,
    at: ago(180),
  },
  'no-changes': {
    kind: 'no-changes',
    commitStatuses: [],
    health: 'unknown',
  },
  'unknown-history': {
    kind: 'unknown-history',
    commitStatuses: [],
    health: 'unknown',
  },
} satisfies Record<string, CellState>;

type Variant = keyof typeof CELLS;

const VARIANTS = Object.keys(CELLS) as Variant[];

interface FlowCellArgs {
  variant: Variant;
  selected: boolean;
  width: number;
  onSelect: () => void;
  onJumpToRow: (rowId: string) => void;
}

const meta: Meta<FlowCellArgs> = {
  title: 'HistoryView/FlowCell',
  argTypes: {
    variant: { control: 'select', options: VARIANTS },
    selected: { control: 'boolean' },
    width: { control: { type: 'number', min: 180, max: 260, step: 10 } },
  },
  args: {
    variant: 'live',
    selected: false,
    width: 220,
    onSelect: fn(),
    onJumpToRow: fn(),
  },
};

export default meta;

type Story = StoryObj<FlowCellArgs>;

function CellSlot({ variant, selected, onSelect, onJumpToRow }: Omit<FlowCellArgs, 'width'>) {
  return (
    <div className="hp-row__env-slot">
      <FlowCell
        cell={CELLS[variant]}
        branch={BRANCH}
        isSelected={selected}
        onSelect={onSelect}
        rowsById={rowsById}
        onJumpToRow={onJumpToRow}
      />
    </div>
  );
}

function Canvas({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="hp"
      style={{ height: 'auto', width: 'max-content', overflow: 'visible', gap: 8, padding: 16 }}
    >
      {children}
    </div>
  );
}

function SingleCell({ width, selected, ...rest }: FlowCellArgs) {
  return (
    <Canvas>
      <div
        className={`hp-row ${selected ? 'hp-row--selected' : ''}`}
        style={{ gridTemplateColumns: `${width}px`, width: 'max-content' }}
      >
        <CellSlot selected={selected} {...rest} />
      </div>
    </Canvas>
  );
}

const single = (variant: Variant): Story => ({
  args: { variant },
  render: (args) => <SingleCell {...args} />,
});

export const Live = single('live');
export const InFlightPrOpen = single('in-flight');
export const InFlightProposed = single('proposed');
export const Failed = single('failed');
export const WasFailed = single('was-failed');
export const WasHere = single('was-here');
export const NoOp = single('no-op');
export const NoChanges = single('no-changes');
export const UnknownHistory = single('unknown-history');

const labelStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  color: '#4a5568',
  alignSelf: 'center',
};

export const AllStates: Story = {
  argTypes: {
    variant: { table: { disable: true } },
    selected: { table: { disable: true } },
  },
  render: ({ width, onSelect, onJumpToRow }) => {
    const gridTemplateColumns = `90px repeat(${VARIANTS.length}, ${width}px)`;
    return (
      <Canvas>
        <div style={{ display: 'grid', gap: 8, padding: '0 9px', gridTemplateColumns }}>
          <span />
          {VARIANTS.map((variant) => (
            <span key={variant} style={labelStyle}>
              {variant}
            </span>
          ))}
        </div>
        {[false, true].map((selected) => (
          <div
            key={String(selected)}
            className={`hp-row ${selected ? 'hp-row--selected' : ''}`}
            style={{ gridTemplateColumns }}
          >
            <span style={labelStyle}>{selected ? 'Selected' : 'Default'}</span>
            {VARIANTS.map((variant) => (
              <CellSlot
                key={variant}
                variant={variant}
                selected={selected}
                onSelect={onSelect}
                onJumpToRow={onJumpToRow}
              />
            ))}
          </div>
        ))}
      </Canvas>
    );
  },
};
