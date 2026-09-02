/**
 * Whether the UI should serve stable mock data instead of live data.
 *
 * Toggled by the `?mock=true` URL query parameter. Reads window.location.search,
 * which works both in the standalone dashboard SPA and inside the Argo CD-hosted
 * app-view extension (Argo CD preserves unknown query params on the app URL).
 */
export function isMockMode(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return new URLSearchParams(window.location.search).get('mock') === 'true';
  } catch {
    return false;
  }
}
