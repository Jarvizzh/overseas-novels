import { useCallback, useEffect, useState } from 'react';

export type ThemeMode = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'color-scheme';
const THEME_SYNC_EVENT = 'cms-theme-sync';

function getSystemScheme(): ResolvedTheme {
  if (typeof window === 'undefined' || !window.matchMedia) return 'light';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function readStoredThemeMode(): ThemeMode {
  try {
    const val = localStorage.getItem(STORAGE_KEY);
    if (val === 'light' || val === 'dark') return val;
    const legacy = localStorage.getItem('cms_theme');
    if (legacy === 'light' || legacy === 'dark') return legacy;
  } catch {
    // Ignore storage access errors
  }
  return 'system';
}

function applyThemeToDocument(mode: ThemeMode): ResolvedTheme {
  const resolved: ResolvedTheme = mode === 'system' ? getSystemScheme() : mode;
  if (typeof document !== 'undefined') {
    const meta = document.querySelector<HTMLMetaElement>('meta[name="color-scheme"]');
    if (meta) {
      meta.content = mode === 'system' ? 'light dark' : mode;
    }
    document.documentElement.setAttribute('data-theme', resolved);
  }
  return resolved;
}

export function useThemeMode() {
  const [mode, setModeState] = useState<ThemeMode>(() => readStoredThemeMode());
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() =>
    applyThemeToDocument(readStoredThemeMode()),
  );

  const setMode = useCallback((next: ThemeMode) => {
    try {
      if (next === 'system') {
        localStorage.removeItem(STORAGE_KEY);
        localStorage.removeItem('cms_theme');
      } else {
        localStorage.setItem(STORAGE_KEY, next);
      }
    } catch {
      // Still apply in-memory even if persistence fails
    }
    setModeState(next);
    setResolvedTheme(applyThemeToDocument(next));
    if (typeof window !== 'undefined') {
      window.dispatchEvent(new CustomEvent<ThemeMode>(THEME_SYNC_EVENT, { detail: next }));
    }
  }, []);

  useEffect(() => {
    setResolvedTheme(applyThemeToDocument(mode));
  }, [mode]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const handleMediaChange = () => {
      if (mode === 'system') {
        setResolvedTheme(applyThemeToDocument('system'));
      }
    };
    const handleStorage = (e: StorageEvent) => {
      if (e.key === STORAGE_KEY || e.key === null) {
        const synced = readStoredThemeMode();
        setModeState(synced);
        setResolvedTheme(applyThemeToDocument(synced));
      }
    };
    const handleSameTabSync = (e: Event) => {
      const next = (e as CustomEvent<ThemeMode>).detail;
      if (next === 'system' || next === 'light' || next === 'dark') {
        setModeState(next);
        setResolvedTheme(applyThemeToDocument(next));
      }
    };
    mq.addEventListener('change', handleMediaChange);
    window.addEventListener('storage', handleStorage);
    window.addEventListener(THEME_SYNC_EVENT, handleSameTabSync);
    return () => {
      mq.removeEventListener('change', handleMediaChange);
      window.removeEventListener('storage', handleStorage);
      window.removeEventListener(THEME_SYNC_EVENT, handleSameTabSync);
    };
  }, [mode]);

  return { mode, resolvedTheme, setMode };
}
