import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const readerStorageSource = await readFile(new URL('../src/utils/readerStorage.ts', import.meta.url), 'utf8');
const source = (await readFile(new URL('../src/utils/api.ts', import.meta.url), 'utf8'))
  .replace("import { readerStorage } from './readerStorage';", readerStorageSource)
  .replace('import.meta.env.VITE_API_BASE_URL', "''");
const output = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
}).outputText;
const { api, registerSessionRefreshHandler } = await import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}`);

globalThis.document = { cookie: '' };
globalThis.localStorage = { getItem: () => null };
globalThis.window = {
  location: { href: 'https://example.invalid' },
  get localStorage() {
    return globalThis.localStorage;
  },
};

test('shelf removal preserves every ID and limits concurrency and batch size', async () => {
  const original = globalThis.fetch;
  const batches = [];
  let active = 0;
  globalThis.fetch = async (_url, init) => {
    assert.equal(++active, 1);
    assert.equal(init.credentials, 'include');
    batches.push(JSON.parse(init.body).novel_ids);
    await Promise.resolve();
    active--;
    return Response.json({ message: 'ok' });
  };
  try {
    const ids = Array.from({ length: 201 }, (_, i) => i + 1);
    await api.removeFromShelf(ids);
    assert.deepEqual(
      batches.map((batch) => batch.length),
      [100, 100, 1],
    );
    assert.deepEqual(batches.flat(), ids);
    await api.removeFromShelf([]);
    assert.equal(batches.length, 3);
  } finally {
    globalThis.fetch = original;
  }
});

test('exit progress schedules all bounded requests before awaiting responses', async () => {
  const original = globalThis.fetch;
  const calls = [];
  const replies = [];
  globalThis.fetch = (_url, init) => {
    calls.push(init);
    return new Promise((resolve) => replies.push(resolve));
  };
  try {
    const updates = Array.from({ length: 201 }, (_, i) => ({
      novel_id: i + 1,
      chapter_index: 0,
      scroll_offset_percentage: 0,
      updated_at: '2026-11-17T00:00:00Z',
    }));
    const pending = api.syncProgress(updates, { userId: 7, keepalive: true });
    assert.equal(calls.length, 3);
    assert.ok(calls.every((call) => call.keepalive && JSON.parse(call.body).user_id === 7));
    const batches = calls.map((call) => JSON.parse(call.body).progress_updates);
    assert.deepEqual(
      batches.map((batch) => batch.length),
      [100, 100, 1],
    );
    assert.deepEqual(batches.flat(), updates);
    for (const resolve of replies) resolve(Response.json({ message: 'ok' }));
    await pending;
  } finally {
    globalThis.fetch = original;
  }
});

test('normal batching stops on failure and preserves the API error', async () => {
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = async () => {
    calls++;
    return Response.json({ error: 'Unavailable' }, { status: 503 });
  };
  try {
    await assert.rejects(api.removeFromShelf(Array.from({ length: 201 }, (_, i) => i + 1)), {
      status: 503,
      message: 'Unavailable',
    });
    assert.equal(calls, 1);
  } finally {
    globalThis.fetch = original;
  }
});

test('expired reading credentials refresh once and retry concurrent requests', async () => {
  const previousFetch = globalThis.fetch;
  const previousStorage = globalThis.localStorage;
  let token = 'expired';
  let refreshes = 0;
  const attempts = [];
  globalThis.localStorage = { getItem: (key) => key === 'auth-token' ? token : null };
  globalThis.fetch = async (url, init) => {
    const bearer = init.headers.get('Authorization');
    attempts.push(bearer);
    if (bearer === 'Bearer expired') return Response.json({ error: 'Expired', code: 'invalid_credentials' }, { status: 401 });
    assert.ok(url.endsWith('?user_id=7'));
    return Response.json({ chapter: { id: 'purchased' }, access_type: 'chapter', locked: false });
  };
  registerSessionRefreshHandler(async () => {
    refreshes++;
    await new Promise((resolve) => setTimeout(resolve, 5));
    token = 'renewed';
    return token;
  });
  try {
    const responses = await Promise.all([api.getChapterContent(1, 0, 7), api.getChapterContent(1, 1, 7)]);
    assert.equal(refreshes, 1);
    assert.equal(attempts.filter((value) => value === 'Bearer expired').length, 2);
    assert.equal(attempts.filter((value) => value === 'Bearer renewed').length, 2);
    assert.ok(responses.every((response) => !response.locked && response.access_type === 'chapter'));
  } finally {
    registerSessionRefreshHandler(null);
    globalThis.localStorage = previousStorage;
    globalThis.fetch = previousFetch;
  }
});

test('failed refresh and a real account change stop reading retries', async () => {
  const previousFetch = globalThis.fetch;
  const previousStorage = globalThis.localStorage;
  let token = 'expired';
  let calls = 0;
  let refreshes = 0;
  globalThis.localStorage = { getItem: (key) => key === 'auth-token' ? token : null };
  globalThis.fetch = async () => {
    calls++;
    return Response.json({ error: 'Expired' }, { status: 401 });
  };
  registerSessionRefreshHandler(async () => { refreshes++; return null; });
  try {
    await assert.rejects(api.getChapterContent(1, 0, 7), { status: 401 });
    assert.equal(calls, 1);
    assert.equal(refreshes, 1);
    calls = 0;
    globalThis.fetch = async () => {
      calls++;
      return token === 'expired'
        ? Response.json({ error: 'Expired' }, { status: 401 })
        : Response.json({ error: 'Account changed', code: 'account_changed' }, { status: 409 });
    };
    registerSessionRefreshHandler(async () => { refreshes++; token = 'other-user'; return token; });
    await assert.rejects(api.getChapterContent(1, 0, 7), { status: 409, code: 'account_changed' });
    assert.equal(calls, 2);
    assert.equal(refreshes, 2);
  } finally {
    registerSessionRefreshHandler(null);
    globalThis.localStorage = previousStorage;
    globalThis.fetch = previousFetch;
  }
});
