import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const source = (await readFile(new URL('../src/utils/api.ts', import.meta.url), 'utf8')).replace(
  'import.meta.env.VITE_API_BASE_URL',
  "''",
);
const output = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
}).outputText;
const { api } = await import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}`);

globalThis.document = { cookie: '' };
globalThis.window = { location: { href: 'https://example.invalid' } };
globalThis.localStorage = { getItem: () => null };

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
    const pending = api.syncProgress(updates, { keepalive: true });
    assert.equal(calls.length, 3);
    assert.ok(calls.every((call) => call.keepalive));
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
