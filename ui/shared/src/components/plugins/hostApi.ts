import type { PromoterPluginsAPI } from '../../types/plugin';
import {
  registerCommitStatusRowPlugin,
  registerCommitStatusRowPluginByAnnotation,
} from './registry';

/**
 * Installs the plugin API on `window` so externally loaded bundles can register
 * themselves. Idempotent, and safe to call before or after plugin scripts load.
 *
 * Both surfaces call this during bootstrap, before requesting plugin bundles.
 * A bundle that somehow runs first would find no API and silently fail to
 * register, which is why this is installed as early as possible rather than
 * lazily on first render.
 */
export function installPluginHostApi(): PromoterPluginsAPI {
  if (typeof window === 'undefined') {
    // Server-side rendering and unit tests without a DOM: the registry still
    // works for internal plugins; there is simply no global to hang it on.
    return { registerCommitStatusRowPlugin, registerCommitStatusRowPluginByAnnotation };
  }
  const existing = window.promoterPluginsAPI;
  if (existing) {
    return existing;
  }
  const api: PromoterPluginsAPI = {
    registerCommitStatusRowPlugin,
    registerCommitStatusRowPluginByAnnotation,
  };
  window.promoterPluginsAPI = api;
  return api;
}
