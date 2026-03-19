/**
 * Icon styles for the GitOps Promoter app view tab. Injected into the document
 * when the extension loads so the tab shows our logo.
 *
 * Styles are generated from docs/assets/logo/icon SVGs by hack/extension-icon-styles.sh.
 */
import { ICON_STYLES } from './iconStyles.generated';

export function injectIconStyles(): void {
  if (typeof document === 'undefined' || !document.head) return;
  const style = document.createElement('style');
  style.textContent = ICON_STYLES;
  style.setAttribute('data-gitops-promoter-extension', 'icon');
  document.head.appendChild(style);
}
