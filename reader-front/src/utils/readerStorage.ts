// The active tab can keep its access token and preferences when Web Storage is
// unavailable. After navigation, the existing session cookie restores login.
const volatileValues = new Map<string, string | null>();

export const readerStorage = {
  getItem(key: string): string | null {
    if (volatileValues.has(key)) return volatileValues.get(key) ?? null;
    try {
      return window.localStorage.getItem(key);
    } catch {
      return null;
    }
  },

  setItem(key: string, value: string): void {
    try {
      window.localStorage.setItem(key, value);
      volatileValues.delete(key);
    } catch {
      volatileValues.set(key, value);
    }
  },

  removeItem(key: string): void {
    try {
      window.localStorage.removeItem(key);
      volatileValues.delete(key);
    } catch {
      volatileValues.set(key, null);
    }
  },
};
