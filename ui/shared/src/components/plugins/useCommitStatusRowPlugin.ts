import { getCommitStatusRowPlugin } from './registry';
import type { RowPlugin } from './types';

/**
 * Returns the row plugin registered for a commit status.
 *
 * Every plugin bundle is expected to have finished registering by the time
 * the app first renders (each surface loads its bundles before mounting), so
 * this reads the registry directly rather than subscribing to later changes.
 */
export function useCommitStatusRowPlugin(
  kind: string | undefined,
  apiVersion?: string,
  annotations?: Record<string, string>,
): RowPlugin | undefined {
  return getCommitStatusRowPlugin(kind, apiVersion, annotations);
}
