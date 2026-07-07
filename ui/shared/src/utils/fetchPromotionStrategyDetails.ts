import type { PromotionStrategyBundle } from '../types/bundle';

const VIEW_GROUP = 'view.promoter.argoproj.io';
const VIEW_KIND = 'PromotionStrategyDetails';
const VIEW_VERSION = 'v1alpha1';

export async function fetchPromotionStrategyDetails(
  appName: string,
  appNamespace: string,
  psNamespace: string,
  psName: string,
): Promise<PromotionStrategyBundle> {
  const params = new URLSearchParams({
    appNamespace,
    namespace: psNamespace,
    resourceName: psName,
    version: VIEW_VERSION,
    kind: VIEW_KIND,
    group: VIEW_GROUP,
  });
  const response = await fetch(`/api/v1/applications/${appName}/resource?${params}`);
  if (!response.ok) {
    let errorText = '';
    try {
      errorText = await response.text();
    } catch {
      // ignore errors while reading error body
    }
    const messageParts = [
      `Request failed with status ${response.status} ${response.statusText}`,
      errorText && `body: ${errorText}`,
    ].filter(Boolean);
    throw new Error(messageParts.join(' - '));
  }
  const data: { manifest: string } = await response.json();
  return JSON.parse(data.manifest) as PromotionStrategyBundle;
}
