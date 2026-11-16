export type ThemePreference = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'color-scheme';

export function getStoredThemePreference(): ThemePreference {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === 'light' || saved === 'dark') {
      return saved;
    }
  } catch {
    // Ignore storage access errors in restricted webviews
  }
  return 'system';
}

export function getSystemTheme(): 'light' | 'dark' {
  if (typeof window !== 'undefined' && window.matchMedia) {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  return 'light';
}

export function resolveTheme(pref: ThemePreference, systemTheme: 'light' | 'dark' = getSystemTheme()): 'light' | 'dark' {
  return pref === 'system' ? systemTheme : pref;
}

export function applyThemePreference(pref: ThemePreference): void {
  const root = document.documentElement;
  const meta = document.querySelector('meta[name="color-scheme"]');

  if (pref === 'system') {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // Ignore storage errors
    }
    root.removeAttribute('data-user-theme');
    if (meta) meta.setAttribute('content', 'light dark');
  } else {
    try {
      localStorage.setItem(STORAGE_KEY, pref);
    } catch {
      // Ignore storage errors
    }
    root.setAttribute('data-user-theme', pref);
    if (meta) meta.setAttribute('content', pref);
  }
}
