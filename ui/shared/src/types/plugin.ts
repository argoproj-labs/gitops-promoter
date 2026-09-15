import type { RowPlugin } from '../components/plugins/types';

/**
 * The API a GitOps Promoter UI plugin bundle calls to register itself.
 *
 * Installed on `window` by both UI surfaces (the standalone dashboard and the
 * Argo CD UI extension) before plugin bundles are loaded, so one bundle
 * registers the same way on either surface. This mirrors Argo CD's
 * `window.extensionsAPI`, which promoter extension authors already know.
 */
export interface PromoterPluginsAPI {
  /**
   * Registers a commit status row plugin for a commit status kind.
   *
   * `group` and `version` default to the promoter CRD group and "any version".
   * A plugin registered for a kind that already has one replaces it, which is
   * how an external bundle overrides a built-in row.
   */
  registerCommitStatusRowPlugin: (
    plugin: RowPlugin,
    kind: string,
    group?: string,
    version?: string,
  ) => void;
}

declare global {
  interface Window {
    promoterPluginsAPI?: PromoterPluginsAPI;
  }
}
