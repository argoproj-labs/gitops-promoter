import { useSyncExternalStore } from 'react';

// Stylesheets follow the host's theme on their own (see styles/theme.scss).
// This is for the few things that need the theme in JS, like picking an Ace
// editor theme. It watches the `theme-dark` class the dashboard puts on <html>;
// Argo CD sets its class on a nested wrapper, which this does not track.
const isDark = (): boolean => document.documentElement.classList.contains('theme-dark');

const subscribe = (onChange: () => void): (() => void) => {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  return () => observer.disconnect();
};

export function useDarkTheme(): boolean {
  return useSyncExternalStore(subscribe, isDark, () => false);
}
