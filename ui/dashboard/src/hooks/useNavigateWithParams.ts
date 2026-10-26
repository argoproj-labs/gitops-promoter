import { useCallback } from 'react';
import { useLocation, useNavigate, type NavigateOptions } from 'react-router';

export const COMMIT_PARAM = 'commit';
export const ENV_PARAM = 'env';
export const NODE_PARAM = 'node';
export const VIEW_PARAM = 'view';

const PAGE_STATE_PARAMS = [COMMIT_PARAM, ENV_PARAM, NODE_PARAM, VIEW_PARAM];

export function carriedSearch(search: string): string {
  const params = new URLSearchParams(search);
  PAGE_STATE_PARAMS.forEach((key) => params.delete(key));
  const rest = params.toString();
  return rest ? `?${rest}` : '';
}

/**
 * Like useNavigate, but preserves the current query string when navigating to a
 * string path that doesn't already carry its own. Keeps params such as ?mock=true
 * intact across in-app navigation (Back button, tile clicks). Page-local state
 * such as ?commit=&env=&node= is not carried over.
 */
export function useNavigateWithParams() {
  const navigate = useNavigate();
  const { search } = useLocation();

  return useCallback(
    (to: string, options?: NavigateOptions) => {
      const carried = carriedSearch(search);
      const next = to.includes('?') || !carried ? to : `${to}${carried}`;
      navigate(next, options);
    },
    [navigate, search],
  );
}
