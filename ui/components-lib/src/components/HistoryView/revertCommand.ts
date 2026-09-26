import type { CellState } from './types';
import { INSTANCE_ID_LABEL } from '@shared/utils/environments';

/**
 * Whether the detail drawer should offer a command that restores this version.
 * Only past active versions (was-here / historical failed, plus a superseded
 * restore) with a hydrated SHA. The live and proposed cells can also be `failed`,
 * but restoring either is not a rollback: the live one is already on the branch,
 * and the proposed one was never promoted, so restoring it would skip its gates.
 */
export function canShowRevertCommand(
  cell: Pick<CellState, 'kind' | 'hydrated' | 'isLive' | 'isProposed'>,
): boolean {
  if (!cell.hydrated?.sha || cell.isLive || cell.isProposed) return false;
  return cell.kind === 'was-here' || cell.kind === 'failed' || cell.kind === 'restored';
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
  /**
   * The ChangeTransferPolicy's instance-id label. A non-default install only watches resources
   * carrying its instance id, so the RevertCommit must carry the same one.
   */
  instanceId?: string;
  branch: string;
  sha: string;
}

/**
 * `kubectl apply` of a RevertCommit for this environment and hydrated commit.
 *
 * Wrapped in `sh -c` so the heredoc is accepted when pasted into bash, zsh, or fish.
 * Creating the resource restores the active branch; deleting it lets promotion pull
 * requests auto-merge again. The reverted change itself may not be proposed again until
 * a new commit lands on the proposed branch.
 */
export function buildRevertCommitApplyCommand(input: RevertCommitApplyInput): string {
  const name = revertCommitResourceName(input.branch, input.sha);
  const manifest = [
    'apiVersion: promoter.argoproj.io/v1alpha1',
    'kind: RevertCommit',
    'metadata:',
    `  name: ${name}`,
    `  namespace: ${input.namespace}`,
    ...(input.instanceId ? ['  labels:', `    ${INSTANCE_ID_LABEL}: "${input.instanceId}"`] : []),
    'spec:',
    '  changeTransferPolicyRef:',
    `    name: ${input.changeTransferPolicyName}`,
    `  sha: ${input.sha}`,
  ].join('\n');

  return `sh -c 'kubectl apply -f - <<EOF\n${manifest}\nEOF'`;
}
