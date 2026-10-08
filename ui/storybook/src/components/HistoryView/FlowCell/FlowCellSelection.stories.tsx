import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import React from 'react';
import FlowCell from '@lib/components/HistoryView/FlowCell/FlowCell';
import DetailDrawer from '@lib/components/HistoryView/DetailDrawer/DetailDrawer';
import { DRAWER_DEFAULT_WIDTH } from '@lib/components/HistoryView/presentation';
import type { CellState, CommitRow, EnvColumn } from '@lib/components/HistoryView/types';
import type { Commit, EnrichedBranchCommitStatus, PullRequest } from '@shared/types/promotion';
import '@lib/components/HistoryView/index.scss';

const REPO_URL = 'https://github.com/argoproj-labs/gitops-promoter';
const DEV = 'environment/dev';
const STAGING = 'environment/staging';
const PROD = 'environment/prod';
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

const failing: EnrichedBranchCommitStatus[] = [
  { key: 'argocd-health', phase: 'success', description: 'Application is Healthy' },
  { key: 'e2e-tests', phase: 'failure', description: '3 of 120 tests failed: checkout-flow' },
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
    commitStatuses: failing,
    health: 'failure',
    pullRequest: mergedPR(141, 60),
    at: ago(60),
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
    noopNote: `Same dry SHA as the previous entry, so ${STAGING} didn't change.`,
    at: ago(180),
  },
} satisfies Record<string, CellState>;

type Variant = keyof typeof CELLS;

const VARIANTS = Object.keys(CELLS) as Variant[];

const ROW_SHA = 'e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6';
const ROW_AT = ago(130);

const rowCells: Record<string, CellState> = {
  [DEV]: { ...CELLS['was-here'], at: ago(130) },
  [STAGING]: { ...CELLS.live, at: ago(95) },
  [PROD]: { ...CELLS['in-flight'], at: ago(12) },
};

const sampleRow: CommitRow = {
  id: ROW_SHA.slice(0, 7),
  dryShaFull: ROW_SHA,
  dryShaShort: ROW_SHA.slice(0, 7),
  subject: 'Raise connection pool size for checkout service',
  author: 'Jane Doe',
  body: 'Raise connection pool size for checkout service.\n\nSigned-off-by: Jane Doe <jane@example.com>',
  prId: '142',
  prUrl: `${REPO_URL}/pull/142`,
  repoUrl: REPO_URL,
  freshestAt: Date.parse(ago(12)),
  earliestAt: Date.parse(ROW_AT),
  cells: rowCells,
  hasLive: true,
  hasInFlight: true,
  hasFailed: false,
  hasNoop: false,
};

const rowsById = new Map<string, CommitRow>([
  [replacerRow.id, replacerRow],
  [sampleRow.id, sampleRow],
]);

const envs: EnvColumn[] = [
  {
    branch: DEV,
    autoMerge: true,
    color: '#1f4f5e',
    liveStatuses: passing,
    liveHealth: 'success',
    proposedStatuses: [],
    proposedHealth: 'unknown',
  },
  {
    branch: STAGING,
    autoMerge: true,
    color: '#6f42c1',
    liveStatuses: passing,
    liveHealth: 'success',
    proposedStatuses: [],
    proposedHealth: 'unknown',
  },
  {
    branch: PROD,
    autoMerge: false,
    color: '#9a5a0f',
    liveStatuses: passing,
    liveHealth: 'success',
    proposedStatuses: pending,
    proposedHealth: 'pending',
  },
];

interface SelectionArgs {
  width: number;
  focusedVariant: Variant;
  containerWidth: number;
  selectedBranch: string;
  onSelect: () => void;
  onJumpToRow: (rowId: string) => void;
  onSelectCell: (branch: string) => void;
}

const meta: Meta<SelectionArgs> = {
  title: 'HistoryView/FlowCell Selection',
  argTypes: {
    width: { control: { type: 'number', min: 180, max: 260, step: 10 } },
    focusedVariant: { control: 'select', options: VARIANTS },
    containerWidth: { control: { type: 'number', min: 480, max: 1400, step: 20 } },
    selectedBranch: { control: 'select', options: [DEV, STAGING, PROD] },
  },
  args: {
    width: 220,
    focusedVariant: 'live',
    containerWidth: 1300,
    selectedBranch: STAGING,
    onSelect: fn(),
    onJumpToRow: fn(),
    onSelectCell: fn(),
  },
};

export default meta;

type Story = StoryObj<SelectionArgs>;

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

const labelStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  color: '#4a5568',
  alignSelf: 'center',
};

const MATRIX_ROWS = [
  { label: 'Default', selected: false, focused: false },
  { label: 'Selected', selected: true, focused: false },
  { label: 'Selected + focus', selected: true, focused: true },
];

export const SelectionMatrix: Story = {
  argTypes: {
    containerWidth: { table: { disable: true } },
    selectedBranch: { table: { disable: true } },
  },
  render: ({ width, focusedVariant, onSelect, onJumpToRow }) => {
    const gridTemplateColumns = `130px repeat(${VARIANTS.length}, ${width}px)`;
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
        {MATRIX_ROWS.map(({ label, selected, focused }) => (
          <div
            key={label}
            className={`hp-row ${selected ? 'hp-row--selected' : ''}`}
            style={{ gridTemplateColumns }}
          >
            <span style={labelStyle}>{label}</span>
            {VARIANTS.map((variant) => (
              <div
                key={variant}
                className="hp-row__env-slot"
                data-focus-target={focused && variant === focusedVariant ? '' : undefined}
              >
                <FlowCell
                  cell={CELLS[variant]}
                  branch={STAGING}
                  isSelected={selected}
                  onSelect={onSelect}
                  rowsById={rowsById}
                  onJumpToRow={onJumpToRow}
                />
              </div>
            ))}
          </div>
        ))}
      </Canvas>
    );
  },
  play: ({ canvasElement }) => {
    canvasElement.querySelector<HTMLElement>('[data-focus-target] .cell')?.focus();
  },
};

export const SelectedInRow: Story = {
  argTypes: {
    width: { table: { disable: true } },
    focusedVariant: { table: { disable: true } },
  },
  render: ({ containerWidth, selectedBranch, onSelect, onJumpToRow }) => (
    <Canvas>
      <div style={{ width: containerWidth, overflowX: 'auto', padding: '6px 0' }}>
        <div
          className="hp-row hp-row--selected"
          style={{
            gridTemplateColumns: 'minmax(280px, 420px) repeat(3, minmax(180px, 260px)) 1fr',
          }}
        >
          <div className="hp-row__commit">
            <div className="hp-row__subject" title={sampleRow.subject}>
              {sampleRow.subject}
            </div>
            <div className="hp-row__meta">
              <span className="hp-row__sha">{sampleRow.dryShaShort}</span>
              <span className="hp-row__sep" aria-hidden="true">
                ·
              </span>
              <span className="hp-row__author-inline">{sampleRow.author}</span>
            </div>
          </div>
          {envs.map((env) => (
            <div key={env.branch} className="hp-row__env-slot">
              <FlowCell
                cell={rowCells[env.branch]}
                branch={env.branch}
                isSelected={env.branch === selectedBranch}
                onSelect={onSelect}
                rowsById={rowsById}
                onJumpToRow={onJumpToRow}
              />
            </div>
          ))}
        </div>
      </div>
    </Canvas>
  ),
};

const noop = () => {};

export const DrawerPresenceList: Story = {
  argTypes: {
    width: { table: { disable: true } },
    focusedVariant: { table: { disable: true } },
    containerWidth: { table: { disable: true } },
  },
  render: ({ selectedBranch, onJumpToRow, onSelectCell }) => (
    <div style={{ position: 'relative', height: 760, width: DRAWER_DEFAULT_WIDTH + 40 }}>
      <DetailDrawer
        row={sampleRow}
        cell={rowCells[selectedBranch]}
        branch={selectedBranch}
        envs={envs}
        rowsById={rowsById}
        width={DRAWER_DEFAULT_WIDTH}
        isResizing={false}
        onResizeStart={noop}
        onResizeReset={noop}
        onResizeTo={noop}
        onClose={noop}
        onJumpToRow={onJumpToRow}
        onSelectCell={onSelectCell}
      />
    </div>
  ),
};
