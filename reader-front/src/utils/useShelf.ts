import { useCallback, useEffect, useRef, useState } from 'react';
import { useToast } from '../context/toastStore';
import { api, type Novel, type SavedProgress } from './api';
import { withRequestTimeout } from './requestTimeout';

type ShelfFeed = {
  owner: number;
  novels: Novel[];
  nextCursor?: string;
  loading: boolean;
  failure?: { cursor?: string };
};

export function useShelf(
  owner: number | undefined,
  token: string | undefined,
  visible: boolean,
  mergePosition: (owner: number, entry: SavedProgress) => void,
) {
  const [membership, setMembership] = useState<{ owner: number; ids: number[] }>();
  const [feed, setFeed] = useState<ShelfFeed>();
  const [revision, setRevision] = useState(0);
  const [editingToken, setEditingToken] = useState<string>();
  const pageRequest = useRef<AbortController | null>(null);
  const idsRequest = useRef<AbortController | null>(null);
  const editRequest = useRef<AbortController | null>(null);
  const { showToast } = useToast();

  const ids = membership?.owner === owner ? membership?.ids : undefined;
  const page = feed?.owner === owner ? feed : undefined;
  const isCurrent = useCallback((signal: AbortSignal) =>
    !signal.aborted && token === localStorage.getItem('auth-token'), [token]);

  useEffect(() => {
    if (!owner || !token) return;
    const controller = new AbortController();
    idsRequest.current = controller;
    void withRequestTimeout(controller.signal, 10_000, (signal) =>
      api.getShelfIDs(owner, { signal, authToken: token }),
    ).then((nextIDs) => {
      if (isCurrent(controller.signal)) setMembership({ owner, ids: nextIDs });
    }).catch((error) => {
      if (isCurrent(controller.signal)) console.error('Failed to load library membership:', error);
    });
    return () => controller.abort();
  }, [owner, token, revision, isCurrent]);

  const loadPage = useCallback(async (cursor?: string) => {
    if (!owner || !token || pageRequest.current) return;
    const controller = new AbortController();
    if (!isCurrent(controller.signal)) return;
    pageRequest.current = controller;
    setFeed((previous) => ({
      owner,
      novels: previous?.owner === owner ? previous.novels : [],
      nextCursor: previous?.owner === owner ? previous.nextCursor : undefined,
      failure: previous?.owner === owner ? previous.failure : undefined,
      loading: true,
    }));
    try {
      const result = await withRequestTimeout(controller.signal, 10_000, (signal) =>
        api.getShelfPage(owner, cursor, { signal, authToken: token }),
      );
      if (!isCurrent(controller.signal)) return;
      for (const item of result.items) {
        mergePosition(owner, { ...item, version: item.progress_version });
      }
      setFeed((previous) => {
        const novels = new Map<number, Novel>();
        if (cursor && previous?.owner === owner) {
          for (const novel of previous.novels) novels.set(novel.id, novel);
        }
        for (const item of result.items) novels.set(item.novel_id, item.novel);
        return { owner, novels: [...novels.values()], nextCursor: result.next_cursor, loading: false };
      });
    } catch {
      if (isCurrent(controller.signal)) {
        setFeed((previous) => previous?.owner === owner
          ? { ...previous, loading: false, failure: { cursor } }
          : { owner, novels: [], loading: false, failure: { cursor } });
      }
    } finally {
      if (pageRequest.current === controller) pageRequest.current = null;
    }
  }, [owner, token, isCurrent, mergePosition]);

  useEffect(() => {
    if (!visible) return;
    void loadPage();
    return () => {
      pageRequest.current?.abort();
      pageRequest.current = null;
    };
  }, [visible, loadPage, revision]);

  useEffect(() => () => {
    editRequest.current?.abort();
    editRequest.current = null;
  }, [token]);

  const edit = useCallback(async (novelIDs: number[], toggle = false) => {
    if (!owner || !token || editRequest.current) return false;
    const controller = new AbortController();
    if (!isCurrent(controller.signal)) return false;
    editRequest.current = controller;
    idsRequest.current?.abort();
    setEditingToken(token);
    try {
      const request = <T,>(run: (options: { signal: AbortSignal; authToken: string }) => Promise<T>) =>
        withRequestTimeout(controller.signal, 10_000, (signal) => run({ signal, authToken: token }));
      const current = ids ?? (toggle ? await request((options) => api.getShelfIDs(owner, options)) : undefined);
      if (!isCurrent(controller.signal)) return false;
      if (toggle && !current?.includes(novelIDs[0])) {
        await request((options) => api.addToShelf(novelIDs[0], options));
        if (isCurrent(controller.signal)) setMembership({ owner, ids: [...(current ?? []), novelIDs[0]] });
      } else {
        await request((options) => api.removeFromShelf(novelIDs, options));
        if (isCurrent(controller.signal)) {
          const removed = new Set(novelIDs);
          if (current) setMembership({ owner, ids: current.filter((id) => !removed.has(id)) });
          setFeed((previous) => previous?.owner === owner
            ? { ...previous, novels: previous.novels.filter((novel) => !removed.has(novel.id)) }
            : previous);
        }
      }
      return isCurrent(controller.signal);
    } catch {
      if (isCurrent(controller.signal)) showToast('Could not update your library. Please try again.', 'error');
      return false;
    } finally {
      if (editRequest.current === controller) {
        editRequest.current = null;
        setEditingToken(undefined);
        // A multi-batch removal may have partially succeeded.
        if (isCurrent(controller.signal)) setRevision((previous) => previous + 1);
      }
    }
  }, [owner, token, ids, isCurrent, showToast]);

  return {
    ids: ids ?? [],
    novels: page?.novels ?? [],
    initialLoading: Boolean(owner && (!page || (page.loading && !page.novels.length))),
    loadingMore: Boolean(page?.loading),
    hasMore: Boolean(page?.nextCursor),
    pageFailed: Boolean(page?.failure),
    editing: Boolean(token && editingToken === token),
    loadMore: () => {
      if (page?.failure || page?.nextCursor) {
        void loadPage(page.failure ? page.failure.cursor : page.nextCursor);
      }
    },
    toggle: (id: number) => edit([id], true),
    remove: edit,
  };
}
