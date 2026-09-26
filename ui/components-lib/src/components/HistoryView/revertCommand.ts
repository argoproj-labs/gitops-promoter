import type { CellKind } from './types';

/**
 * Whether the detail drawer should offer a command that restores this version.
 * Only past active versions (was-here / historical failed, plus a superseded
 * restore) with a hydrated SHA.
 */
export function canShowRevertCommand(kind: CellKind, hydratedSha?: string): boolean {
  if (!hydratedSha) return false;
  return kind === 'was-here' || kind === 'failed' || kind === 'restored';
}

/** DNS-1123 label: lowercase, hyphens for everything else, no leading or trailing hyphen. */
function dns1123(value: string): string {
  const cleaned = value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return cleaned || 'x';
}

/**
 * RevertCommit metadata.name. Includes the environment branch and a short sha so two
 * environments restoring the same commit, or one environment restored to two commits,
 * do not collide. Stays inside the 253-character DNS-1123 subdomain limit.
 */
export function revertCommitResourceName(branch: string, sha: string): string {
  const prefix = 'revert-';
  const suffix = `-${sha.slice(0, 7)}`;
  const budget = 253 - prefix.length - suffix.length;
  let stem = dns1123(branch);
  if (stem.length > budget) {
    stem = stem.slice(0, budget).replace(/-+$/g, '') || 'x';
  }
  return `${prefix}${stem}${suffix}`;
}

export interface RevertCommitApplyInput {
  namespace: string;
  changeTransferPolicyName: string;
  branch: string;
  sha: string;
}

/**
 * `kubectl apply` of a RevertCommit for this environment and hydrated commit.
 *
 * Wrapped in `sh -c` so the heredoc is accepted when pasted into bash, zsh, or fish.
 * Creating the resource restores the active branch; deleting it lets the promotion
 * pull request merge again.
 */
export function buildRevertCommitApplyCommand(input: RevertCommitApplyInput): string {
  const name = revertCommitResourceName(input.branch, input.sha);
  const manifest = [
    'apiVersion: promoter.argoproj.io/v1alpha1',
    'kind: RevertCommit',
    'metadata:',
    `  name: ${name}`,
    `  namespace: ${input.namespace}`,
    'spec:',
    '  changeTransferPolicyRef:',
    `    name: ${input.changeTransferPolicyName}`,
    `  sha: ${input.sha}`,
  ].join('\n');

  return `sh -c 'kubectl apply -f - <<EOF\n${manifest}\nEOF'`;
}
