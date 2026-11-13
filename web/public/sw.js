// SPDX-License-Identifier: AGPL-3.0-or-later

// The service worker of the web interface (decision 65). It serves the app's own hashed files from a cache, so the app starts
// fast, and it answers a document navigation that the network cannot reach with the offline page. It keeps nothing else: no
// API answer, no file content, no signed link, no share page, no range answer, nothing from another origin. The build replaces
// the placeholder below with an identifier of the build (its files' names and contents), so a new build is a new cache. The
// files of a share from the system share sheet (decision 82) are the one thing it holds, in memory, until the page takes them. The
// cache of the build before is kept, so a tab that is still on it can load its files; older caches are removed.
//
// The rules are in storable(), fallbackAllowed() and the listeners. The unit tests in src/lib/pwa/sw.test.ts load this file in
// a stub of the worker's scope and prove each refusal, so a change here that lets a person's data into the cache fails there.

const BUILD = '__SELFCLOUD_BUILD__';
const PREFIX = 'selfcloud-';
const SHELL = PREFIX + BUILD;
// STATE holds the signed-out marker, which is written at sign-out and removed at sign-in; while it is there the worker stores
// no app file. It also holds the identifier of the build that was last activated, so the cache of the build before can be kept.
const STATE = PREFIX + 'state';
const MARK = '/signed-out';
const BUILD_MARK = '/build';
const OFFLINE = '/offline.html';
// OFFLINE_FILES are the offline page and the three files it loads. They are fetched at install and at activate, and answered
// from the cache, so the page shows offline, styled and with its words.
const OFFLINE_FILES = ['/offline.html', '/offline.css', '/offline.js'];
// TYPES are the media types the build's own files are served with. Any other answer under /assets/ is not stored.
const TYPES = ['text/javascript', 'application/javascript', 'text/css', 'font/woff2', 'font/woff', 'image/svg+xml', 'image/png', 'image/webp', 'image/jpeg'];
// REFUSED are the paths that are never a page the worker answers: the API, the office endpoints, the share pages, the brand
// files and the manifest. A navigation there goes to the network, and a failed one fails.
const REFUSED = ['/api', '/wopi', '/s', '/brand', '/manifest'];
// SHARE_PATH is the share target (decision 82). A form post there, from the system share sheet, is read by the worker and held in
// memory only: this code never writes the files to a cache or to storage. The page at SHARE_PAGE takes them once, by message.
const SHARE_PATH = '/share-target';
const SHARE_PAGE = '/share';
// SHARE_CAP is how many shares the worker holds at once. At the cap a new share drops the oldest one no page has taken, so a
// share from another site cannot keep a real share out (decision 82).
const SHARE_CAP = 4;
// SHARE_FILE_CAP is how many files one share keeps. It counts files, not bytes: request.formData() reads the whole body before the
// worker can count anything, so the cap does not limit what is read. It bounds the number of files kept, for a share sheet's
// multi-select of photos, and the files past it are not kept and the page says how many (decision 82, rounds 2 and 3).
const SHARE_FILE_CAP = 100;
// UNCLAIMED_LIMIT is how long a held share is kept while no page has taken it: sixty seconds. The page takes a share as soon as it
// opens, so this is the time a share sheet's hand-off has to finish. Chromium ends an event that runs longer than five minutes
// ("event dispatched to service workers has a 5 minute timeout in the Chrome implementation", service_worker.mojom in the Chromium
// source), so it is well inside that.
const UNCLAIMED_LIMIT = 60 * 1000;

// storable reports whether a response to request may be cached: a plain 200 GET of one of the build's own files under /assets/,
// with no query string and no Range header, that was not redirected, was served from the path that was asked for, and has one
// of the build's media types.
function storable(request, response, origin) {
  if (request.method !== 'GET') return false;
  if (!response || response.status !== 200 || response.type !== 'basic' || response.redirected) return false;
  if (request.headers && request.headers.has && request.headers.has('range')) return false;
  const url = new URL(request.url);
  if (url.origin !== origin || url.search !== '') return false;
  if (!url.pathname.startsWith('/assets/')) return false;
  if (/[%\\]|\/\/|(^|\/)\.\.(\/|$)/.test(url.pathname)) return false;
  try {
    if (new URL(response.url).pathname !== url.pathname) return false;
  } catch {
    return false;
  }
  const type = (response.headers.get('content-type') || '').split(';')[0].trim().toLowerCase();
  return TYPES.includes(type);
}

// fallbackAllowed reports whether a failed request may be answered with the offline page: a document navigation, outside the
// paths the worker never answers.
function fallbackAllowed(request, url) {
  if (request.mode !== 'navigate' || request.destination !== 'document') return false;
  return !REFUSED.some((p) => url.pathname === p || url.pathname.startsWith(p + '/') || url.pathname.startsWith(p + '.'));
}

// precache fetches the offline page and its files into the current cache. It runs at install, at activate and when a person
// signs in, and each time the copy is fetched again, so a changed brand or a changed file reaches the worker.
function precache() {
  return caches.open(SHELL).then((cache) => cache.addAll(OFFLINE_FILES.map((f) => new Request(f, { cache: 'reload' }))));
}

self.addEventListener('install', (event) => {
  event.waitUntil(precache());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(activate());
});

// activate keeps the current cache and the one of the build before it, removes the rest, and fetches the offline page again.
async function activate() {
  const state = await caches.open(STATE);
  const previous = await state.match(BUILD_MARK).then((r) => (r ? r.text() : null));
  await state.put(BUILD_MARK, new Response(BUILD));
  const keep = new Set([SHELL, STATE]);
  if (previous && previous !== BUILD) keep.add(PREFIX + previous);
  const keys = await caches.keys();
  await Promise.all(keys.filter((k) => k.startsWith(PREFIX) && !keep.has(k)).map((k) => caches.delete(k)));
  await precache().catch(() => undefined);
  await self.clients.claim();
}

// signedOut reads the marker. A missing state cache is not signed out.
async function signedOut() {
  try {
    const state = await caches.open(STATE);
    return (await state.match(MARK)) !== undefined;
  } catch {
    return false;
  }
}

async function cacheFirst(request) {
  const cache = await caches.open(SHELL);
  const hit = await cache.match(request);
  if (hit) return hit;
  const response = await fetch(request);
  if (storable(request, response, self.location.origin) && !(await signedOut())) {
    // A full disk or a refused write leaves the answer uncached; the person still gets the file.
    Promise.resolve(cache.put(request, response.clone())).catch(() => undefined);
  }
  return response;
}

// offlineFile answers one of the offline page's files from the cache, and asks the network only when it is not there.
async function offlineFile(request) {
  const cache = await caches.open(SHELL);
  return (await cache.match(request)) || fetch(request);
}

// navigate is the network first. Only when the network fails is the offline page answered, and the navigation's own answer
// is never stored: it carries a nonce and, for a share page, a secret in its address.
async function navigate(request) {
  try {
    return await fetch(request);
  } catch (err) {
    const cache = await caches.open(SHELL);
    const offline = await cache.match(OFFLINE);
    if (offline) return offline;
    throw err;
  }
}

// isShare reports whether a request is the share sheet's post: a top-level navigation (not a frame or other embedded request), on
// this origin, to exactly the share target.
function isShare(request, url) {
  return (
    request.mode === 'navigate' &&
    request.destination === 'document' &&
    url.origin === self.location.origin &&
    url.pathname === SHARE_PATH
  );
}

// shares holds the shares the worker keeps, by id, in memory only. Each holds its files, the timer that expires it and the release
// of the wait that keeps the worker open for it. A Map keeps insertion order, so its first key is the oldest share.
const shares = new Map();

// endShare forgets one share: its files, its timer, and the wait that kept the worker open for it.
function endShare(id) {
  const share = shares.get(id);
  if (!share) return;
  clearTimeout(share.timer);
  shares.delete(id);
  share.release();
}

// holdShare keeps the files under a fresh random id and returns the id. At the cap, the oldest unclaimed share is dropped first.
function holdShare(files, release) {
  while (shares.size >= SHARE_CAP) endShare(shares.keys().next().value);
  const id = crypto.randomUUID();
  const timer = setTimeout(() => endShare(id), UNCLAIMED_LIMIT);
  shares.set(id, { files, timer, release });
  return id;
}

// webOrigin returns the origin of an address when it is an http or https address, normalised (the scheme and host in lower case, the
// default port left out), and null for anything else: a non-web scheme such as an Android app's, about:client, or text that is not an
// address at all. Only a web address can be another site, so only its origin is compared with this site's (decision 82).
function webOrigin(value) {
  try {
    const url = new URL(value);
    if (url.protocol === 'http:' || url.protocol === 'https:') return url.origin;
  } catch {
    // Not an address: null.
  }
  return null;
}

// innerAddress returns the address a blob: or filesystem: URL wraps, as text: blob:https://evil.test/uuid wraps https://evil.test/uuid. A
// page of another site can make such a URL, and the browser reports it as the referrer, so the wrapped address is what is compared
// (decision 82). Any other text is returned as it is.
function innerAddress(value) {
  return /^(blob|filesystem):/i.test(value) ? value.slice(value.indexOf(':') + 1) : value;
}

// fromAnotherSite reports whether a share post came from a page of another web site. Two signals are read, and each refuses only a
// web address of another site (decision 82, rounds 2 to 5).
// (1) The Origin header. It is what the worker can see of where a post came from (measured in headless Chrome): a post from a
// page of another web site carries that site's origin. An Origin that is absent, the string "null" or a non-web scheme (such as an
// Android app's) is not refused: the system share sheet's own Origin was not measured.
// (2) The referrer. An empty referrer is accepted. A referrer that is an http or https address of another site is refused, and so is
// a blob: or filesystem: referrer that wraps one. A referrer with a non-web scheme (an Android app's, or about:client) is accepted, as
// is one that cannot be read as an address (the check fails open). A page can send no referrer at all, so the Share page also asks the
// person to confirm before anything is saved, and both signals are defence in depth only.
function fromAnotherSite(request) {
  const here = self.location.origin;
  const origin = request.headers && typeof request.headers.get === 'function' ? request.headers.get('origin') : null;
  const fromOrigin = origin ? webOrigin(origin) : null;
  if (fromOrigin !== null && fromOrigin !== here) return true;
  const referrer = request.referrer;
  if (!referrer) return false;
  const fromReferrer = webOrigin(innerAddress(referrer));
  return fromReferrer !== null && fromReferrer !== here;
}

// refusedShare answers a share the worker will not keep: 303 to the share page, with no id and nothing held.
function refusedShare() {
  return Response.redirect(new URL(SHARE_PAGE, self.location.origin).href, 303);
}

// receiveShare answers the share sheet's post with 303 to the share page, under the id of the files it holds. With no file it holds
// nothing and sends the person to the page without an id. Its wait ends when the files are handed over or expire.
async function receiveShare(request, release) {
  let files = [];
  try {
    const form = await request.formData();
    files = form.getAll('files').filter((f) => typeof f !== 'string');
  } catch {
    files = [];
  }
  if (files.length === 0) {
    release();
    return refusedShare();
  }
  // A share keeps at most SHARE_FILE_CAP files; the rest are counted, so the page can say so.
  const kept = files.slice(0, SHARE_FILE_CAP);
  const skipped = files.length - kept.length;
  const page = new URL(SHARE_PAGE, self.location.origin);
  page.search = '?id=' + holdShare(kept, release) + (skipped > 0 ? '&skipped=' + skipped : '');
  return Response.redirect(page.href, 303);
}

// handOver gives the files of one share to the page that asked with its id, once. An unknown id gets nothing and keeps the others.
function handOver(id, port) {
  if (!port) return;
  const share = typeof id === 'string' ? shares.get(id) : undefined;
  if (!share) {
    port.postMessage({ files: [] });
    return;
  }
  const files = share.files;
  endShare(id);
  port.postMessage({ files });
}

self.addEventListener('fetch', (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method === 'POST') {
    if (isShare(request, url)) {
      if (fromAnotherSite(request)) {
        event.respondWith(refusedShare());
        return;
      }
      // The wait is opened before the answer, so the worker stays alive until the page takes the files.
      let release;
      event.waitUntil(new Promise((resolve) => (release = resolve)));
      event.respondWith(receiveShare(request, release));
    }
    return;
  }
  if (request.method !== 'GET') return;
  if (url.origin !== self.location.origin) return;
  if (request.mode === 'navigate') {
    if (fallbackAllowed(request, url)) event.respondWith(navigate(request));
    return;
  }
  if (OFFLINE_FILES.includes(url.pathname) && url.search === '') {
    event.respondWith(offlineFile(request));
    return;
  }
  if (url.pathname.startsWith('/assets/') && url.search === '' && !request.headers.has('range')) {
    event.respondWith(cacheFirst(request));
  }
});

// signIn lifts the signed-out marker, whichever order the messages came in, and fetches the offline page again. It runs for a
// person who signs in: the page sends PRECACHE then, and only then.
async function signIn() {
  const state = await caches.open(STATE);
  await state.delete(MARK);
  await precache();
}

// clearAll removes the app's caches, except the state, and writes the signed-out marker. The page asks for this at sign-out.
async function clearAll() {
  // A sign-out also forgets the files of every share that was not handed over (decision 82).
  for (const id of [...shares.keys()]) endShare(id);
  const keys = await caches.keys();
  await Promise.all(keys.filter((k) => k.startsWith(PREFIX) && k !== STATE).map((k) => caches.delete(k)));
  const state = await caches.open(STATE);
  await state.put(MARK, new Response('signed out'));
}

// The page's requests are handled one at a time, in the order they arrive, and each carries a sequence number. A request older
// than the newest of the other kind is ignored: a sign-out that the page sent before a sign-in must not write the marker back
// after the sign-in has been handled, and a sign-in that is older than a sign-out must not lift it.
let chain = Promise.resolve();
const newest = { CLEAR_CACHES: 0, PRECACHE: 0 };

function accept(type, seq) {
  const other = type === 'CLEAR_CACHES' ? 'PRECACHE' : 'CLEAR_CACHES';
  if (typeof seq !== 'number' || seq < newest[other]) return false;
  newest[type] = Math.max(newest[type], seq);
  return true;
}

self.addEventListener('message', (event) => {
  const data = event.data || {};
  if (data.type === 'SKIP_WAITING') {
    self.skipWaiting();
    return;
  }
  // The page takes one share's files by id, over the port it sends, and drops them when it refuses or cancels (decision 82).
  if (data.type === 'SHARE_TAKE') {
    handOver(data.id, event.ports && event.ports[0]);
    return;
  }
  if (data.type === 'SHARE_DROP') {
    if (typeof data.id === 'string') endShare(data.id);
    return;
  }
  // The page drops every share it holds when the person changes, the viewed-as person changes, or the server ends the session.
  if (data.type === 'SHARE_DROP_ALL') {
    for (const id of [...shares.keys()]) endShare(id);
    return;
  }
  if (data.type !== 'CLEAR_CACHES' && data.type !== 'PRECACHE') return;
  const job = chain
    .then(() => {
      if (!accept(data.type, data.seq)) return undefined;
      return data.type === 'CLEAR_CACHES' ? clearAll() : signIn();
    })
    .catch(() => undefined);
  chain = job;
  event.waitUntil(job);
});
