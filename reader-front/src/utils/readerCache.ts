import type { ChapterContentResponse } from './api';

const DB_NAME = 'StarNovelReaderCacheDB';
const DB_VERSION = 1;
const STORE_NAME = 'chapters';

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (!('indexedDB' in window)) {
      reject(new Error('IndexedDB not supported'));
      return;
    }

    const request = indexedDB.open(DB_NAME, DB_VERSION);

    request.onupgradeneeded = (event) => {
      const db = (event.target as IDBOpenDBRequest).result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: 'key' });
      }
    };

    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

export const readerCache = {
  async getCachedChapter(novelId: number, chapterIndex: number): Promise<ChapterContentResponse | null> {
    try {
      const db = await openDB();
      const key = `${novelId}-${chapterIndex}`;
      return new Promise((resolve) => {
        const tx = db.transaction(STORE_NAME, 'readonly');
        const store = tx.objectStore(STORE_NAME);
        const req = store.get(key);
        req.onsuccess = () => {
          if (req.result && req.result.data) {
            resolve(req.result.data as ChapterContentResponse);
          } else {
            resolve(null);
          }
        };
        req.onerror = () => resolve(null);
      });
    } catch {
      return null;
    }
  },

  async setCachedChapter(novelId: number, chapterIndex: number, data: ChapterContentResponse): Promise<void> {
    try {
      const db = await openDB();
      const key = `${novelId}-${chapterIndex}`;
      return new Promise((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, 'readwrite');
        const store = tx.objectStore(STORE_NAME);
        const req = store.put({ key, data, timestamp: Date.now() });
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
    } catch (err) {
      console.warn('Failed to save chapter to IndexedDB cache:', err);
    }
  },

  async prefetchChapters(
    novelId: number,
    currentIndex: number,
    totalCount: number,
    fetchFn: (novelId: number, index: number) => Promise<ChapterContentResponse>
  ): Promise<void> {
    const prefetchCount = 2;
    for (let i = 1; i <= prefetchCount; i++) {
      const targetIndex = currentIndex + i;
      if (targetIndex >= totalCount) break;

      try {
        const cached = await this.getCachedChapter(novelId, targetIndex);
        if (!cached) {
          const res = await fetchFn(novelId, targetIndex);
          if (res) {
            await this.setCachedChapter(novelId, targetIndex, res);
          }
        }
      } catch (err) {
        console.warn(`Background prefetch failed for chapter ${targetIndex}:`, err);
      }
    }
  },
};
