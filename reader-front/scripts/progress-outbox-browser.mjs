import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, extname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Exercise the built H5 and native IndexedDB using a private headless profile.
const project = dirname(dirname(fileURLToPath(import.meta.url)));
const chromePath = process.env.CHROME_PATH ?? [
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome', '/usr/bin/chromium',
].find(existsSync);
assert.ok(chromePath, 'Set CHROME_PATH to a Chrome or Chromium executable');
const scratch = await mkdtemp(join(tmpdir(), 'reader-outbox-browser-'));
const clients = [];
let chrome;
let server;
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function until(check, label, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await check()) return;
    await pause(50);
  }
  throw new Error('Timed out waiting for ' + label);
}

function connect(url) {
  const socket = new WebSocket(url);
  const ready = once(socket, 'open');
  let sequence = 0;
  const waiting = new Map();
  socket.addEventListener('message', ({ data }) => {
    const response = JSON.parse(data);
    if (!response.id) return;
    const item = waiting.get(response.id);
    waiting.delete(response.id);
    if (response.error) item.reject(new Error(response.error.message));
    else item.resolve(response.result);
  });
  const client = {
    close: () => socket.close(),
    async send(method, params = {}) {
      await ready;
      return new Promise((resolve, reject) => {
        waiting.set(++sequence, { resolve, reject });
        socket.send(JSON.stringify({ id: sequence, method, params }));
      });
    },
    async eval(expression) {
      const result = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? 'Browser exception');
      return result.result.value;
    },
  };
  clients.push(client);
  return client;
}

const initial = { novel_id: 1, chapter_index: 2, scroll_offset_percentage: .65,
  updated_at: '2026-01-01T00:00:00.000Z', version: '1' };
const saved = new Map([[1, initial]]);
const requests = [];
const successful = [];
let mode = 'success';
let releaseRequest;
const novel = { id: 1, title: 'Outbox test', author: 'Author', cover: '', status: 'Ongoing',
  publish_status: 'published', synopsis: 'Test', genres: [], words: 20000, views: 0 };
const chapters = Array.from({ length: 4 }, (_, index) => ({
  id: 'chapter-' + index, novel_id: 1, chapter_index: index, title: 'Chapter ' + (index + 1),
  word_count: 5000, price: 0, is_paid: false, content_version: 1,
}));
const body = Array.from({ length: 80 }, (_, index) =>
  'Paragraph ' + index + ' ' + 'The story continued into the evening. '.repeat(8)).join('\n\n');

const faultScript = `
  window.__outboxFaults = JSON.parse(sessionStorage.getItem('outbox-faults') || '{}');
  sessionStorage.removeItem('outbox-faults');
  window.__outboxCalls = {};
  for (const [prototype, method] of [
    [IDBIndex.prototype, 'getAll'], [IDBObjectStore.prototype, 'put'], [IDBObjectStore.prototype, 'delete']
  ]) {
    const original = prototype[method];
    prototype[method] = function (...args) {
      const store = this.objectStore ?? this;
      if (store.name === 'pending' && store.transaction.db.name === 'StarNovelReadingProgress') {
        window.__outboxCalls[method] = (window.__outboxCalls[method] || 0) + 1;
        if (window.__outboxFaults[method] > 0) {
          window.__outboxFaults[method]--;
          throw new DOMException('Injected transient IndexedDB fault', 'UnknownError');
        }
      }
      return original.apply(this, args);
    };
  }
`;

try {
  const build = spawnSync('npm', ['run', 'build', '--', '--outDir', join(scratch, 'dist')], {
    cwd: project, env: { ...process.env, VITE_API_BASE_URL: '/api/v1' }, encoding: 'utf8',
  });
  assert.equal(build.status, 0, build.stdout + build.stderr);
  server = createServer(async (request, response) => {
    const url = new URL(request.url, 'http://localhost');
    const path = url.pathname;
    const user = Number(/^Bearer u(\d+)$/.exec(request.headers.authorization ?? '')?.[1] ?? 0);
    const profile = { id: user || 1, nickname: 'Reader ' + (user || 1), email: null, status: 1 };
    const json = (value, status = 200) =>
      response.writeHead(status, { 'Content-Type': 'application/json' }).end(JSON.stringify(value));
    if (path === '/api/v1/auth/profile') return json(profile);
    if (path === '/api/v1/auth/refresh' || path === '/api/v1/auth/guest') return json({ token: 'u' + profile.id, user: profile });
    if (path === '/api/v1/wallet/balance') return json({ charged_coins: 100, bonus_coins: 0 });
    if (path.endsWith('/history/cursor')) return json({ items: [] });
    if (path === '/api/v1/shelf') return json([]);
    if (path === '/api/v1/novels') return json([novel]);
    if (path === '/api/v1/novels/1') return json(novel);
    if (path === '/api/v1/novels/1/chapters') return json(chapters);
    if (path.startsWith('/api/v1/novels/1/chapters/')) {
      const chapter = chapters[Number(path.split('/').at(-1))];
      return chapter
        ? json({ chapter: { ...chapter, content: body }, content_version: 1, locked: false, price: 0, access_type: 'free' })
        : json({ error: 'Missing chapter' }, 404);
    }
    if (path === '/api/v1/shelf/progress/1') {
      return Number(url.searchParams.get('user_id')) === user
        ? json(saved.get(user) ?? null)
        : json({ code: 'account_changed', error: 'Account changed' }, 409);
    }
    if (path === '/api/v1/shelf/sync') {
      let raw = '';
      for await (const chunk of request) raw += chunk;
      const payload = JSON.parse(raw);
      requests.push({ user, payload });
      if (user !== payload.user_id) return json({ code: 'account_changed' }, 409);
      if (mode === '503') return json({ error: 'Temporary failure' }, 503);
      if (mode === 'hold') {
        mode = '503';
        await new Promise((resolve) => { releaseRequest = resolve; });
      }
      if (mode === 'partial' && payload.progress_updates[0].novel_id > 1100) return json({ error: 'Later batch failed' }, 503);
      for (const item of payload.progress_updates) {
        const previous = saved.get(user);
        if (item.novel_id === 1 && (!previous || item.updated_at >= previous.updated_at)) saved.set(user, item);
      }
      successful.push({ user, payload });
      return json({ message: 'Synced' });
    }
    if (path.startsWith('/api/')) return json({});
    if (path === '/fixture') return response.writeHead(200, { 'Content-Type': 'text/html' }).end('<!doctype html><title>Fixture</title>');
    const extension = extname(path);
    const file = join(scratch, 'dist', extension ? path.substring(1) : 'index.html');
    try {
      const content = await readFile(file);
      response.writeHead(200, { 'Content-Type': ({ '.js': 'application/javascript', '.css': 'text/css' })[extension] ?? 'text/html' });
      response.end(content);
    } catch {
      response.writeHead(404).end();
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const profile = join(scratch, 'profile');
  chrome = spawn(chromePath, [
    '--headless=new', '--no-first-run', '--no-default-browser-check', '--disable-background-networking',
    '--remote-debugging-port=0', '--user-data-dir=' + profile, 'about:blank',
  ], { stdio: 'ignore' });
  let debugPort;
  await until(async () => {
    try { debugPort = (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; return true; }
    catch { return false; }
  }, 'private Chrome profile');
  async function tab() {
    const response = await fetch('http://127.0.0.1:' + debugPort + '/json/new?about:blank', { method: 'PUT' });
    const client = connect((await response.json()).webSocketDebuggerUrl);
    await client.send('Page.enable');
    await client.send('Network.enable');
    await client.send('Page.addScriptToEvaluateOnNewDocument', { source: faultScript });
    return client;
  }
  const first = await tab();
  const navigate = (client, path) => client.send('Page.navigate', { url: origin + path });
  const heading = (title) => until(() => first.eval(`document.querySelector('article h1')?.textContent === ${JSON.stringify(title)}`), title);
  const scroll = (ratio) => first.eval(`(() => {
    const area = document.querySelector('.reader-scroll-area');
    area.scrollTop = (area.scrollHeight - area.clientHeight) * ${ratio};
    area.dispatchEvent(new Event('scroll'));
  })()`);
  const ratio = () => first.eval(`(() => { const area = document.querySelector('.reader-scroll-area');
    return area.scrollTop / (area.scrollHeight - area.clientHeight); })()`);
  const entries = (client, user) => client.eval(`new Promise((resolve, reject) => {
    const request = indexedDB.open('StarNovelReadingProgress', 1);
    request.onsuccess = () => {
      const db = request.result;
      const req = db.transaction('pending').objectStore('pending').index('user').getAll(${user});
      req.onsuccess = () => { resolve(req.result); db.close(); };
      req.onerror = () => reject(req.error);
    };
    request.onerror = () => reject(request.error);
  })`);
  async function seed(client, data) {
    await client.eval(`new Promise((resolve, reject) => {
      const request = indexedDB.open('StarNovelReadingProgress', 1);
      request.onupgradeneeded = () => {
        const store = request.result.createObjectStore('pending', {keyPath: ['userId', 'novel_id']});
        store.createIndex('user', 'userId');
      };
      request.onsuccess = () => {
        const db = request.result;
        const tx = db.transaction('pending', 'readwrite');
        tx.oncomplete = () => { db.close(); resolve(true); };
        tx.onabort = () => reject(tx.error);
        for (const item of ${JSON.stringify(data)}) tx.objectStore('pending').put(item);
      };
      request.onerror = () => reject(request.error);
    })`);
  }

  await navigate(first, '/fixture');
  await until(() => first.eval('location.pathname === "/fixture" && document.readyState === "complete"'), 'fixture');
  await first.eval("localStorage.setItem('auth-token','u1')");
  const stored = { ...initial, chapter_index: 1, scroll_offset_percentage: .8, updated_at: new Date().toISOString(), version: 'persisted', userId: 1 };
  await seed(first, [stored]);
  await first.eval("sessionStorage.setItem('outbox-faults',JSON.stringify({getAll:1}))");
  await navigate(first, '/');
  await until(() => first.eval('window.__outboxCalls?.getAll === 1'), 'injected list failure');
  assert.equal(successful.length, 0);
  // No new progress, visibility or online event is used to recover this read.
  await until(() => Promise.resolve(successful.some((item) => item.payload.progress_updates.some((entry) => entry.version === 'persisted'))), 'automatic storage-read retry');
  await until(async () => (await entries(first, 1)).length === 0, 'persisted acknowledgement');
  console.log('PASS native IndexedDB list failure automatically retries without an external wake');

  await navigate(first, '/fixture');
  await until(() => first.eval('location.pathname === "/fixture" && document.readyState === "complete"'), 'restore fixture');
  const local = { ...stored, chapter_index: 2, scroll_offset_percentage: .77, version: 'restore-newer', updated_at: new Date().toISOString() };
  await seed(first, [local]);
  mode = '503';
  await first.eval("sessionStorage.setItem('outbox-faults',JSON.stringify({getAll:2}))");
  await navigate(first, '/content?novel_id=1');
  await until(() => first.eval("!!document.querySelector('[role=status]')"), 'local restore error feedback');
  assert.equal(await first.eval("!!document.querySelector('article h1')"), false);
  await heading('Chapter 3');
  await until(async () => Math.abs(await ratio() - .77) < .02, 'latest local offset after storage recovery');
  console.log('PASS reader restoration waits for readable local state and restores the newer record');

  mode = 'success';
  await first.eval("window.dispatchEvent(new Event('online'))");
  await until(async () => (await entries(first, 1)).length === 0, 'initial pending progress drained');
  await first.eval("window.__outboxFaults.put=1");
  const acceptedBefore = successful.length;
  await scroll(.42);
  await until(() => first.eval('window.__outboxFaults.put === 0'), 'put failure');
  await until(() => Promise.resolve(successful.length > acceptedBefore && successful.some((item) => item.payload.progress_updates.some((entry) => Math.abs(entry.scroll_offset_percentage - .42) < .02))), 'memory fallback after put failure');
  await first.eval("window.__outboxFaults.delete=1");
  await scroll(.44);
  await until(() => first.eval('window.__outboxFaults.delete === 0'), 'ack failure');
  const retained = await entries(first, 1);
  assert.ok(retained.some((item) => Math.abs(item.scroll_offset_percentage - .44) < .02));
  await until(async () => (await entries(first, 1)).length === 0, 'safe retry after acknowledgement failure');
  console.log('PASS put failure uses memory, acknowledgement failure keeps a retryable durable version');

  mode = 'hold';
  releaseRequest = undefined;
  await scroll(.55);
  await first.eval("window.dispatchEvent(new Event('online'))");
  await until(() => Promise.resolve(Boolean(releaseRequest)), 'in-flight progress');
  await scroll(.71);
  await until(async () => (await entries(first, 1)).some((item) => Math.abs(item.scroll_offset_percentage - .71) < .02), 'newer progress while sending');
  releaseRequest();
  await pause(200);
  assert.ok((await entries(first, 1)).some((item) => Math.abs(item.scroll_offset_percentage - .71) < .02));
  await first.send('Network.emulateNetworkConditions', { offline: true, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  await scroll(.83);
  await until(async () => (await entries(first, 1)).some((item) => Math.abs(item.scroll_offset_percentage - .83) < .02), 'offline progress persisted');
  await first.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  await navigate(first, '/content?novel_id=1');
  await heading('Chapter 3');
  await until(async () => Math.abs(await ratio() - .83) < .02, 'reopened page retains offline offset');
  console.log('PASS concurrent save, 503, offline persistence and page reopening');

  const second = await tab();
  await navigate(second, '/fixture');
  await until(() => second.eval('document.readyState === "complete" && location.pathname === "/fixture"'), 'second tab');
  await second.eval("localStorage.setItem('auth-token','u2')");
  await pause(200);
  mode = 'success';
  await navigate(first, '/content?novel_id=1&chapter_index=2');
  await heading('Chapter 2');
  await scroll(.49);
  await until(() => Promise.resolve(saved.get(2)?.chapter_index === 1), 'account 2 progress');
  assert.ok((await entries(first, 1)).some((item) => Math.abs(item.scroll_offset_percentage - .83) < .02));
  await second.eval("localStorage.setItem('auth-token','u1')");
  await until(() => Promise.resolve(Math.abs((saved.get(1)?.scroll_offset_percentage ?? 0) - .83) < .02), 'account 1 queue resumed');
  assert.ok(successful.every((item) => item.user === item.payload.user_id));
  console.log('PASS two tabs, account isolation and explicit chapter-link precedence');

  const batches = Array.from({ length: 201 }, (_, index) => ({
    ...stored, novel_id: 1001 + index, version: String(index + 1), updated_at: new Date().toISOString(),
  }));
  await seed(first, batches);
  mode = 'partial';
  await first.eval("window.dispatchEvent(new Event('online'))");
  await until(async () => (await entries(first, 1)).length === 101, 'first successful batch acknowledged');
  mode = 'success';
  await first.eval("window.dispatchEvent(new Event('online'))");
  await until(async () => (await entries(first, 1)).length === 0, 'remaining batches automatically drain');
  console.log('PASS partial-batch success clears only acknowledged versions');
} finally {
  if (releaseRequest) releaseRequest();
  for (const client of clients) client.close();
  server?.closeAllConnections();
  if (server) await new Promise((resolve) => server.close(resolve));
  if (chrome && chrome.exitCode === null) {
    const stopped = once(chrome, 'exit');
    chrome.kill();
    await stopped;
  }
  await rm(scratch, { recursive: true, force: true });
}
