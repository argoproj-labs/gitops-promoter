import { useCallback } from 'react';
import { useLocation, useNavigate, type NavigateOptions } from 'react-router';

export const CROSS_PAGE_PARAMS = ['mock', 'namespace'] as const;

export function mergeAllowedParams(to: string, search: string): string {
  if (to.includes('?') || !search) {
    return to;
  }

  const current = new URLSearchParams(search);
  const kept = new URLSearchParams();
  for (const name of CROSS_PAGE_PARAMS) {
    for (const value of current.getAll(name)) {
      kept.append(name, value);
    }
  }

  const query = kept.toString();
  return query ? `${to}?${query}` : to;
}

export function useNavigateWithParams() {
  const navigate = useNavigate();
  const { search } = useLocation();

  return useCallback(
    (to: string, options?: NavigateOptions) => {
      navigate(mergeAllowedParams(to, search), options);
    },
    [navigate, search],
  );
}
