import type { ProgressUpdate } from './api';

const STORE = 'pending';
let opening: Promise<IDBDatabase> | undefined;

function openOutbox(): Promise<IDBDatabase> {
  if (!opening) {
    opening = new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open('StarNovelReadingProgress', 1);
      request.onupgradeneeded = () => {
        const store = request.result.createObjectStore(STORE, { keyPath: ['userId', 'novel_id'] });
        store.createIndex('user', 'userId');
      };
      request.onsuccess = () => {
        const db = request.result;
        db.onversionchange = () => {
          db.close();
          opening = undefined;
        };
        resolve(db);
      };
      request.onerror = () => reject(request.error);
    }).catch((error) => {
      opening = undefined;
      throw error;
    });
  }
  return opening;
}

type PendingProgress = ProgressUpdate & { userId: number };

async function write(
  userId: number,
  entries: ProgressUpdate[],
  update: (store: IDBObjectStore, entry: PendingProgress, current?: PendingProgress) => void,
): Promise<void> {
  const db = await openOutbox();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, 'readwrite');
    tx.oncomplete = () => resolve();
    tx.onabort = () => reject(tx.error);
    const store = tx.objectStore(STORE);
    for (const entry of entries) {
      const request = store.get([userId, entry.novel_id]);
      request.onsuccess = () => update(store, { ...entry, userId }, request.result as PendingProgress | undefined);
    }
  });
}

export const progressOutbox = {
  async list(userId: number): Promise<ProgressUpdate[]> {
    const db = await openOutbox();
    return new Promise((resolve, reject) => {
      const request = db.transaction(STORE).objectStore(STORE).index('user').getAll(userId);
      request.onsuccess = () => resolve((request.result as PendingProgress[]).map(({ userId: _userId, ...entry }) => entry));
      request.onerror = () => reject(request.error);
    });
  },

  put(userId: number, update: ProgressUpdate) {
    return write(userId, [update], (store, entry, current) => {
      if (!current || entry.updated_at > current.updated_at ||
          (entry.updated_at === current.updated_at && entry.version > current.version)) {
        store.put(entry);
      }
    });
  },

  acknowledge(userId: number, entries: ProgressUpdate[]) {
    return write(userId, entries, (store, entry, current) => {
      if (current?.version === entry.version) {
        store.delete([userId, entry.novel_id]);
      }
    });
  },
};
