import { useCallback, useEffect, useRef } from 'react';
import { api, type ProgressUpdate } from './api';
import { progressOutbox } from './progressOutbox';
import { withRequestTimeout } from './requestTimeout';

const BATCH_SIZE = 100;
const DELAY_MS = 2000;
const MAX_RETRY_MS = 60_000;

export function useProgressOutbox(userId: number | undefined, token: string | undefined) {
  const memory = useRef(new Map<number, Map<number, ProgressUpdate>>());
  const wake = useRef<() => void>(() => {});
  const lastTimestamp = useRef(0);

  const pending = useCallback(async (owner: number): Promise<{ entries: ProgressUpdate[]; storageFailed: boolean }> => {
    let entries: ProgressUpdate[] = [];
    let storageFailed = false;
    try {
      entries = await progressOutbox.list(owner);
    } catch {
      storageFailed = true;
    }
    const merged = new Map(entries.map((item) => [item.novel_id, item]));
    for (const entry of memory.current.get(owner)?.values() ?? []) {
      const saved = merged.get(entry.novel_id);
      if (!saved || entry.updated_at > saved.updated_at ||
          (entry.updated_at === saved.updated_at && entry.version > saved.version)) {
        merged.set(entry.novel_id, entry);
      }
    }
    return { entries: [...merged.values()], storageFailed };
  }, []);

  const enqueue = useCallback((input: Omit<ProgressUpdate, 'updated_at' | 'version'>): ProgressUpdate | undefined => {
    if (!userId) return;
    const timestamp = Math.max(Date.now(), lastTimestamp.current + 1);
    lastTimestamp.current = timestamp;
    const version = Array.from(crypto.getRandomValues(new Uint32Array(4))).join('-');
    const entry = { ...input, version, updated_at: new Date(timestamp).toISOString() };
    const byNovel = memory.current.get(userId) ?? new Map<number, ProgressUpdate>();
    memory.current.set(userId, byNovel);
    byNovel.set(input.novel_id, entry);
    void progressOutbox.put(userId, entry).then(() => {
      if (byNovel.get(input.novel_id)?.version === version) byNovel.delete(input.novel_id);
      wake.current();
    }).catch((err) => {
      console.error('Failed to store reading progress locally:', err);
      wake.current();
    });
    return entry;
  }, [userId]);

  useEffect(() => {
    const owner = userId ?? 0;
    const authToken = token;
    if (!owner || !authToken) return;
    const session = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let running = false;
    let queued = false;
    let retry = 0;

    function schedule(delay: number) {
      if (session.signal.aborted || !navigator.onLine) return;
      if (timer !== undefined) return;
      timer = setTimeout(() => {
        timer = undefined;
        void flush(false);
      }, delay);
    }

    async function flush(keepalive: boolean) {
      if (running || session.signal.aborted || !navigator.onLine || authToken !== localStorage.getItem('auth-token')) return;
      running = true;
      queued = false;
      try {
        const { entries, storageFailed } = await pending(owner);
        // Keep the exit request well below the browser's shared keepalive body limit.
        const limit = keepalive ? Math.min(BATCH_SIZE, entries.length) : entries.length;
        for (let offset = 0; offset < limit && !session.signal.aborted; offset += BATCH_SIZE) {
          if (authToken !== localStorage.getItem('auth-token')) break;
          const batch = entries.slice(offset, offset + BATCH_SIZE);
          await withRequestTimeout(session.signal, 10_000, (signal) =>
            api.syncProgress(batch, { userId: owner, authToken, signal, keepalive }),
          );
          const byNovel = memory.current.get(owner);
          for (const item of batch) {
            if (byNovel?.get(item.novel_id)?.version === item.version) byNovel.delete(item.novel_id);
          }
          // Keep acknowledgement failures on the retry path, including when
          // the server has already committed this idempotent progress update.
          await progressOutbox.acknowledge(owner, batch);
        }
        if (storageFailed) throw new Error('Reading progress storage is temporarily unavailable');
        retry = 0;
      } catch {
        retry = Math.min(retry + 1, 6);
      } finally {
        running = false;
        if (retry) schedule(Math.min(DELAY_MS * 2 ** (retry - 1), MAX_RETRY_MS));
        else if (queued) schedule(DELAY_MS);
      }
    }

    function onWake() {
      queued = true;
      if (!running && !retry) schedule(DELAY_MS);
    }
    function onOnline() {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      retry = 0;
      if (running) queued = true;
      else schedule(0);
    }
    function onExit() {
      void flush(true);
    }
    function onVisibility() {
      if (document.visibilityState === 'hidden') onExit();
      else onOnline();
    }
    wake.current = onWake;
    schedule(0);
    window.addEventListener('online', onOnline);
    window.addEventListener('pagehide', onExit);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      session.abort();
      if (timer !== undefined) clearTimeout(timer);
      wake.current = () => {};
      window.removeEventListener('online', onOnline);
      window.removeEventListener('pagehide', onExit);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [pending, token, userId]);

  return { enqueue, pending };
}
