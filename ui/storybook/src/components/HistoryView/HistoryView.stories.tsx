import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn, userEvent, within } from 'storybook/test';
import React from 'react';
import HistoryView from '@lib/components/HistoryView/HistoryView';
import type { Commit, Environment, History, PromotionStrategy } from '@shared/types/promotion';
import '@lib/components/HistoryView/index.scss';

const REPO_URL = 'https://github.com/argoproj-labs/gitops-promoter-demo';
const DEV = 'environment/dev';
const STAGING = 'environment/staging';
const PROD = 'environment/prod';

const now = new Date();

function localTime(daysAgo: number, hours: number, minutes: number): Date {
  const d = new Date(now);
  d.setDate(d.getDate() - daysAgo);
  d.setHours(hours, minutes, 0, 0);
  return d;
}

function today(minutesAgo: number): Date {
  const startOfToday = localTime(0, 0, 0).getTime();
  return new Date(
    Math.max(now.getTime() - minutesAgo * 60_000, startOfToday + (600 - minutesAgo) * 1000),
  );
}

function lastYear(): Date {
  const d = localTime(0, 15, 20);
  d.setFullYear(d.getFullYear() - 1);
  return d;
}

interface CommitSpec {
  sha: string;
  subject: string;
  author: string;
  time?: Date;
}

const COMMITS = {
  c01: {
    sha: 'a01c',
    subject: 'Bump api image to v2.14.1',
    author: 'Ana Lopez',
    time: today(25),
  },
  c02: {
    sha: 'b02d',
    subject: 'Tune HPA thresholds for checkout',
    author: 'Ben Ito',
    time: today(120),
  },
  c03: {
    sha: 'c03e',
    subject: 'Add readiness probe to worker',
    author: 'Cara Diaz',
    time: today(240),
  },
  c04: {
    sha: 'd04f',
    subject: 'Rotate database credentials secret ref',
    author: 'Dev Patel',
    time: localTime(1, 16, 40),
  },
  c05: {
    sha: 'e05a',
    subject: 'Enable canary analysis for payments',
    author: 'Ana Lopez',
    time: localTime(1, 10, 15),
  },
  c06: {
    sha: 'f06b',
    subject: 'Raise memory limit for search',
    author: 'Eli Novak',
    time: localTime(2, 0, 5),
  },
  c07: {
    sha: 'a07c',
    subject: 'Pin ingress controller chart',
    author: 'Ben Ito',
    time: localTime(3, 23, 55),
  },
  c08: {
    sha: 'b08d',
    subject: 'Switch log shipping to OTLP',
    author: 'Cara Diaz',
    time: localTime(3, 14, 30),
  },
  c09: {
    sha: 'c09e',
    subject: 'Add PodDisruptionBudget for api',
    author: 'Dev Patel',
    time: localTime(5, 9, 0),
  },
  c11: { sha: 'e11a', subject: 'Initial kustomize layout', author: 'Eli Novak', time: lastYear() },
  c12: { sha: 'f12b', subject: 'Import legacy manifests', author: 'Ana Lopez' },
} satisfies Record<string, CommitSpec>;

function fullSha(prefix: string): string {
  return prefix.padEnd(40, '0');
}

function commit(spec: CommitSpec): Commit {
  return {
    sha: fullSha(spec.sha),
    subject: spec.subject,
    author: `${spec.author} <${spec.author.toLowerCase().replace(' ', '.')}@example.com>`,
    repoURL: REPO_URL,
    commitTime: spec.time?.toISOString(),
  };
}

function checks(phase: 'success' | 'failure' | 'pending') {
  return [{ key: 'argocd-health', phase }];
}

let prCounter = 100;

function mergedPr(spec: CommitSpec, mergeDelayMinutes: number) {
  prCounter += 1;
  return {
    id: String(prCounter),
    url: `${REPO_URL}/pull/${prCounter}`,
    state: 'merged',
    prMergeTime: spec.time
      ? new Date(spec.time.getTime() + mergeDelayMinutes * 60_000).toISOString()
      : undefined,
  };
}

function openPr(spec: CommitSpec) {
  prCounter += 1;
  return {
    id: String(prCounter),
    url: `${REPO_URL}/pull/${prCounter}`,
    state: 'open',
    prCreationTime: spec.time?.toISOString(),
  };
}

function entry(
  spec: CommitSpec,
  mergeDelayMinutes: number,
  phase: 'success' | 'failure' = 'success',
): History {
  return {
    active: { dry: commit(spec), commitStatuses: checks(phase) },
    pullRequest: spec.time ? mergedPr(spec, mergeDelayMinutes) : undefined,
  };
}

const { c01, c02, c03, c04, c05, c06, c07, c08, c09, c11, c12 } = COMMITS;

const environments: Environment[] = [
  {
    branch: DEV,
    lastHealthyDryShas: [],
    active: { dry: commit(c02), commitStatuses: checks('success') },
    proposed: { dry: commit(c01), commitStatuses: checks('pending') },
    pullRequest: openPr(c01),
    history: [entry(c02, 10), entry(c03, 10), entry(c04, 10), entry(c05, 10), entry(c06, 10)],
  },
  {
    branch: STAGING,
    lastHealthyDryShas: [],
    active: { dry: commit(c03), commitStatuses: checks('success') },
    proposed: { dry: commit(c02), commitStatuses: checks('pending') },
    pullRequest: openPr(c02),
    history: [
      entry(c03, 40),
      entry(c04, 40, 'failure'),
      entry(c05, 40),
      entry(c06, 40),
      entry(c09, 40),
    ],
  },
  {
    branch: PROD,
    lastHealthyDryShas: [],
    active: { dry: commit(c06), commitStatuses: checks('success') },
    proposed: { dry: commit(c05), commitStatuses: checks('failure') },
    pullRequest: openPr(c05),
    history: [
      entry(c06, 120),
      entry(c07, 120),
      entry(c08, 120, 'failure'),
      entry(c11, 120),
      entry(c12, 0),
    ],
  },
];

const strategy: PromotionStrategy = {
  apiVersion: 'promoter.argoproj.io/v1alpha1',
  kind: 'PromotionStrategy',
  metadata: { name: 'checkout-service', namespace: 'promoter-demo' },
  spec: {
    gitRepositoryRef: { name: 'checkout-service' },
    orderCommitStatusRef: { group: 'promoter.argoproj.io', kind: 'CommitStatus', name: 'order' },
    environments: [
      { branch: DEV, autoMerge: true },
      { branch: STAGING, autoMerge: true },
      { branch: PROD, autoMerge: false },
    ],
  },
  status: { environments },
};

interface HistoryViewStoryArgs {
  width?: number;
  height: number;
}

const meta: Meta<HistoryViewStoryArgs> = {
  title: 'HistoryView/Date grouping',
  argTypes: {
    width: { control: { type: 'number', min: 480, max: 1600, step: 20 } },
    height: { control: { type: 'number', min: 320, max: 1200, step: 20 } },
  },
  args: {
    height: 640,
  },
  parameters: {
    layout: 'fullscreen',
  },
  render: ({ width, height }) => (
    <div style={{ width: width ?? '100%', height }}>
      <HistoryView strategy={strategy} onBack={fn()} onSelectionChange={fn()} />
    </div>
  ),
};

export default meta;

type Story = StoryObj<HistoryViewStoryArgs>;

export const Default: Story = {};

export const OldestFirst: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole('button', { name: 'Sort' }));
    const menu = within(canvasElement.ownerDocument.body);
    await userEvent.click(await menu.findByRole('option', { name: /Oldest first/ }));
  },
};

export const Narrow: Story = {
  args: {
    width: 700,
  },
};
