import { useCallback } from 'react';
import { useLocation, useNavigate, type NavigateOptions } from 'react-router';

/**
 * Query params that are global to the app rather than to a single page, and so
 * survive in-app navigation. Only cross-page params belong here; per-page view
 * state (history filters, sort, selection, ...) must not be added.
 */
export const CROSS_PAGE_PARAMS = ['mock'] as const;

/**
 * Builds the destination for useNavigateWithParams: carries the allowlisted
 * cross-page params of `search` over to `to`, dropping everything else. A
 * destination that already carries its own query string is left untouched.
 */
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

/**
 * Like useNavigate, but carries the allowlisted cross-page query params
 * (CROSS_PAGE_PARAMS, e.g. ?mock=true) over to a string destination that does
 * not already carry its own query string. Per-page params are dropped.
 */
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
