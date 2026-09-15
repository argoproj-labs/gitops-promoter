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
