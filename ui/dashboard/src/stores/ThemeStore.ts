import { create } from 'zustand';

export type Theme = 'light' | 'dark';

// Keep in sync with the inline script in index.html, which applies the theme
// before first paint.
export const THEME_STORAGE_KEY = 'gitops-promoter:theme';

// `matchMedia` is missing in some non-browser environments (jsdom).
const darkQuery = (): MediaQueryList | undefined =>
  typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : undefined;

const systemTheme = (): Theme => (darkQuery()?.matches ? 'dark' : 'light');

const readChoice = (): Theme | null => {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    return stored === 'light' || stored === 'dark' ? stored : null;
  } catch {
    return null;
  }
};

const writeChoice = (theme: Theme): void => {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // Storage can be unavailable (private mode, blocked site data); the choice
    // then lasts for this page load only.
  }
};

// The stylesheets key off the same `theme-*` class Argo CD uses, so the
// explorer renders identically in the dashboard and the extension.
const applyTheme = (theme: Theme): void => {
  const { classList } = document.documentElement;
  classList.toggle('theme-dark', theme === 'dark');
  classList.toggle('theme-light', theme === 'light');
};

// The OS/browser preference is the default. Once the user toggles, their
// choice is remembered and the system preference is no longer followed.
export const themeStore = create<{
  theme: Theme;
  chosen: boolean;
  toggle: () => void;
}>((set, get) => ({
  theme: readChoice() ?? systemTheme(),
  chosen: readChoice() !== null,
  toggle: () => {
    const theme = get().theme === 'dark' ? 'light' : 'dark';
    writeChoice(theme);
    set({ theme, chosen: true });
  },
}));

applyTheme(themeStore.getState().theme);
themeStore.subscribe(({ theme }) => applyTheme(theme));

darkQuery()?.addEventListener('change', () => {
  if (!themeStore.getState().chosen) themeStore.setState({ theme: systemTheme() });
});
