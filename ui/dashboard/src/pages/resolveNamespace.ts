export function resolveNamespace(
  urlNamespace: string | null,
  persistedNamespace: string,
  namespaces: string[],
): string {
  if (!urlNamespace) {
    if (namespaces.length && !namespaces.includes(persistedNamespace)) {
      return '';
    }
    return persistedNamespace;
  }
  if (namespaces.length && !namespaces.includes(urlNamespace)) {
    return namespaces.includes(persistedNamespace) ? persistedNamespace : '';
  }
  return urlNamespace;
}
