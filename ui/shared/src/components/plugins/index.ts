import type { CommitStatusManagerKind } from '../../types/promotion';
import type { RowPlugin } from './types';
import TimedCommitStatus from './TimedCommitStatus/TimedCommitStatus';
import { registerCommitStatusRowPlugin } from './registry';

export const commitStatusPlugins: Partial<Record<CommitStatusManagerKind, RowPlugin>> = {
  TimedCommitStatus,
};

// Internal plugins are registered at module load so they are in place before any
// externally loaded bundle runs, letting an external plugin override a built-in
// row by registering the same kind later.
for (const [kind, plugin] of Object.entries(commitStatusPlugins) as [
  CommitStatusManagerKind,
  RowPlugin,
][]) {
  registerCommitStatusRowPlugin(plugin, kind);
}

export type { CommitStatusContext, RowPlugin } from './types';
export type { PromoterPluginsAPI } from '../../types/plugin';
// resetPluginRegistry is deliberately not re-exported here: it is a test helper,
// and this module is the surface external plugin authors consume.
export {
  ANY_VERSION,
  PROMOTER_GROUP,
  getCommitStatusRowPlugin,
  listCommitStatusRowPlugins,
  listCommitStatusRowPluginsByAnnotation,
  registerCommitStatusRowPlugin,
  registerCommitStatusRowPluginByAnnotation,
} from './registry';
export type {
  CommitStatusRowPluginAnnotationRegistration,
  CommitStatusRowPluginRegistration,
} from './registry';
export { installPluginHostApi } from './hostApi';
export { useCommitStatusRowPlugin } from './useCommitStatusRowPlugin';
export { assertSharedReactInstance } from './reactSharing';
export { PluginErrorBoundary } from './PluginErrorBoundary';
