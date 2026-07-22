import { useCallback } from 'react';
import { useLocation, useNavigate, type NavigateOptions } from 'react-router-dom';

/**
 * Like useNavigate, but preserves the current query string when navigating to a
 * string path that doesn't already carry its own. Keeps params such as ?mock=true
 * intact across in-app navigation (Back button, tile clicks, history links).
 */
export function useNavigateWithParams() {
  const navigate = useNavigate();
  const { search } = useLocation();

  return useCallback(
    (to: string, options?: NavigateOptions) => {
      const next = to.includes('?') || !search ? to : `${to}${search}`;
      navigate(next, options);
    },
    [navigate, search],
  );
}
