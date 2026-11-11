// Runs the rendered service worker against an in-memory Cache API and a scripted
// network, then prints what each scenario served offline as JSON. The Go test
// owns the assertions; this file only drives the worker the way a browser would.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const ORIGIN = 'https://training.example.test';
const source = readFileSync(process.argv[2], 'utf8');
const settle = () => new Promise((resolve) => setTimeout(resolve, 5));

class FakeCache {
  constructor() { this.entries = new Map(); }
  key(request) { return typeof request === 'string' ? new URL(request, ORIGIN).href : request.url; }
  async put(request, response) { this.entries.set(this.key(request), response); }
  async match(request) {
    const hit = this.entries.get(this.key(request));
    return hit ? hit.clone() : undefined;
  }
  async addAll(requests) {
    for (const r of requests) this.entries.set(this.key(r), new Response('asset'));
  }
}

class FakeCacheStorage {
  constructor() { this.caches = new Map(); }
  async open(name) {
    if (!this.caches.has(name)) this.caches.set(name, new FakeCache());
    return this.caches.get(name);
  }
  async keys() { return [...this.caches.keys()]; }
  async delete(name) { return this.caches.delete(name); }
  async has(name) { return this.caches.has(name); }
  async match(request, options = {}) {
    for (const [name, cache] of this.caches) {
      if (options.cacheName && options.cacheName !== name) continue;
      const hit = await cache.match(request);
      if (hit) return hit;
    }
    return undefined;
  }
}

function boot() {
  const handlers = {};
  const network = { online: true, next: null };
  const caches = new FakeCacheStorage();
  const self = {
    location: { origin: ORIGIN },
    addEventListener: (type, fn) => { handlers[type] = fn; },
    skipWaiting: async () => {},
    clients: { claim: async () => {} },
  };
  const fetch = async () => {
    if (!network.online) throw new TypeError('offline');
    const { body, status = 200, headers = {} } = network.next || {};
    return new Response(body ?? '', { status, headers });
  };
  const context = vm.createContext({ self, caches, fetch, Response, Request, Headers, URL, Promise, Date, console, setTimeout });
  vm.runInContext(source, context);

  async function lifecycle(type) {
    const waits = [];
    handlers[type]({ waitUntil: (p) => waits.push(p) });
    await Promise.all(waits);
  }

  // get asks the worker for a page, as a navigation would, and returns the body
  // it served, or null when it served nothing.
  async function get(path, reply) {
    network.next = reply;
    const waits = [];
    let served;
    handlers.fetch({
      request: new Request(ORIGIN + path),
      respondWith: (p) => { served = p; },
      waitUntil: (p) => waits.push(p),
    });
    let body = null;
    try {
      const res = await served;
      body = res ? await res.text() : null;
    } catch (e) {
      body = null;
    }
    await Promise.all(waits);
    await settle();
    return body;
  }

  return {
    caches, get, lifecycle,
    offline: () => { network.online = false; },
    online: () => { network.online = true; },
  };
}

const future = () => Math.floor(Date.now() / 1000) + 3600;
const past = () => Math.floor(Date.now() / 1000) - 10;
const as = (partition, body, status = 200) => ({ body, status, headers: { 'X-Cache-Partition': partition } });

const scenarios = {
  // B signs in on a device A used: offline, B sees B's pages and none of A's.
  async switchUser(sw) {
    await sw.get('/', as(`aaaa-${future()}`, 'A today'));
    await sw.get('/history', as(`aaaa-${future()}`, 'A history'));
    await sw.get('/', as(`bbbb-${future()}`, 'B today'));
    sw.offline();
    return { today: await sw.get('/'), history: await sw.get('/history'), caches: await sw.caches.keys() };
  },
  // The logged-out page wipes the signed-in pages.
  async logout(sw) {
    await sw.get('/', as(`aaaa-${future()}`, 'A today'));
    await sw.get('/logged-out', { body: 'bye', headers: { 'X-Cache-Clear': '1' } });
    sw.offline();
    return { today: await sw.get('/'), caches: await sw.caches.keys() };
  },
  // A session that has ended leaves nothing readable offline.
  async expired(sw) {
    await sw.get('/', as(`aaaa-${past()}`, 'A today'));
    sw.offline();
    return { today: await sw.get('/'), caches: await sw.caches.keys() };
  },
  // Errors are never kept as the offline copy.
  async errors(sw) {
    await sw.get('/', as(`aaaa-${future()}`, 'A today'));
    await sw.get('/', as(`aaaa-${future()}`, 'boom', 500));
    sw.offline();
    return { today: await sw.get('/') };
  },
  // A malformed partition name is ignored rather than trusted.
  async malformed(sw) {
    await sw.get('/', as('../evil', 'odd'));
    sw.offline();
    return { today: await sw.get('/'), caches: await sw.caches.keys() };
  },
  // The owner signed in at the root keeps the secret mount's offline copy.
  async secretBesideRoot(sw) {
    await sw.get('/app/', { body: 'owner today' });
    await sw.get('/', as(`aaaa-${future()}`, 'A today'));
    sw.offline();
    return { today: await sw.get('/app/') };
  },
  // The secret mount sends no partition and keeps its old offline copy.
  async secretMount(sw) {
    await sw.get('/app/', { body: 'owner today' });
    sw.offline();
    return { today: await sw.get('/app/') };
  },
};

const results = {};
for (const [name, run] of Object.entries(scenarios)) {
  const sw = boot();
  await sw.lifecycle('install');
  await sw.lifecycle('activate');
  results[name] = await run(sw);
}
process.stdout.write(JSON.stringify(results));
