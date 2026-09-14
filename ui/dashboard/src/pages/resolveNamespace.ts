/**
 * URL wins over the persisted namespace, but only once the namespace list has
 * loaded and confirms the URL value exists. While the list is empty the URL
 * value is taken at face value; an unknown one falls through to the persisted
 * value once the list arrives.
 */
export function resolveNamespace(
  urlNamespace: string | null,
  persistedNamespace: string,
  namespaces: string[],
): string {
  if (!urlNamespace) {
    return persistedNamespace;
  }
  if (namespaces.length && !namespaces.includes(urlNamespace)) {
    return persistedNamespace;
  }
  return urlNamespace;
}
