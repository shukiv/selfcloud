// SPDX-License-Identifier: AGPL-3.0-or-later

// The service worker (public/sw.js, decision 65) runs here in a stub of a worker's scope. Each rule it keeps is proved by a test
// that drives its listeners: what is cached, what is refused, the offline page and where it may be given, the sign-out marker,
// the order of the page's messages, and which old caches are removed.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { runInNewContext } from "node:vm";
import { describe, expect, it, vi } from "vitest";

const ORIGIN = "https://drive.test";
const SHELL = "selfcloud-build1";
const STATE = "selfcloud-state";
// Vitest runs from the web directory, where public/ is.
const SOURCE = readFileSync(resolve(process.cwd(), "public/sw.js"), "utf8").replace("__SELFCLOUD_BUILD__", "build1");

interface Req {
  url: string;
  method: string;
  mode: string;
  destination: string;
  headers: { has(name: string): boolean };
  // formData is the body of a form post, as the browser reads it for the worker (decision 82).
  formData?: () => Promise<FormData>;
  // referrer is the page that made the request: "" when the browser sent none, as the system share sheet does.
  referrer?: string;
}

interface Res {
  status: number;
  type: string;
  ok: boolean;
  redirected: boolean;
  url: string;
  headers: { get(name: string): string | null };
  body: string;
  clone(): Res;
  text(): Promise<string>;
}

function absolute(url: string): string {
  return new URL(url, ORIGIN).href;
}

function request(url: string, opts: { method?: string; mode?: string; destination?: string; range?: boolean } = {}): Req {
  const headers = new Set<string>(opts.range ? ["range"] : []);
  return {
    url: absolute(url),
    method: opts.method ?? "GET",
    mode: opts.mode ?? "no-cors",
    destination: opts.destination ?? "script",
    headers: { has: (name: string) => headers.has(name.toLowerCase()) },
  };
}

// shareRequest is the share sheet's form post: a navigation to the share target with the files in a multipart form.
function shareRequest(
  files: File[],
  opts: { url?: string; mode?: string; method?: string; referrer?: string; destination?: string } = {},
): Req {
  const form = new FormData();
  for (const f of files) form.append("files", f);
  return {
    ...request(opts.url ?? "/share-target", {
      method: opts.method ?? "POST",
      mode: opts.mode ?? "navigate",
      destination: opts.destination ?? "document",
    }),
    formData: async () => form,
    referrer: opts.referrer ?? "",
  };
}

// response is an answer as the browser gives it. Its url is where the bytes came from, and redirected is true when the browser
// followed a redirect to get them; the content type defaults to the type of a script.
function response(
  status = 200,
  type = "basic",
  body = "app",
  extra: { url?: string; redirected?: boolean; contentType?: string | null; location?: string } = {},
): Res {
  const headers = new Map<string, string>();
  if (extra.contentType !== null) headers.set("content-type", extra.contentType ?? "text/javascript; charset=utf-8");
  if (extra.location !== undefined) headers.set("location", extra.location);
  const r: Res = {
    status,
    type,
    ok: status >= 200 && status < 300,
    redirected: extra.redirected ?? false,
    url: extra.url ?? "",
    headers: { get: (name: string) => headers.get(name.toLowerCase()) ?? null },
    body,
    clone: () => r,
    text: async () => body,
  };
  return r;
}

class FakeCache {
  entries = new Map<string, Res>();
  puts: string[] = [];
  constructor(private network: (r: Req) => Promise<Res>) {}
  async match(r: Req | string): Promise<Res | undefined> {
    return this.entries.get(typeof r === "string" ? absolute(r) : r.url);
  }
  async put(r: Req | string, res: Res): Promise<void> {
    const key = typeof r === "string" ? absolute(r) : r.url;
    this.puts.push(key);
    this.entries.set(key, res);
  }
  async add(r: Req): Promise<void> {
    const res = await this.network(r);
    if (!res.ok) throw new Error("add failed");
    this.entries.set(r.url, res);
  }
  async delete(r: Req | string): Promise<boolean> {
    return this.entries.delete(typeof r === "string" ? absolute(r) : r.url);
  }
  async addAll(requests: Req[]): Promise<void> {
    for (const r of requests) await this.add(r);
  }
}

// load runs the worker in a fresh scope. network answers the requests the worker sends on; the answer's url is filled in from
// the request, as a server's answer is, unless the test sets one.
function load(network: (r: Req) => Promise<Res> = async () => response()) {
  const listeners: Record<string, Array<(event: unknown) => void>> = {};
  const stores = new Map<string, FakeCache>();
  let skipped = 0;
  const served = async (r: Req) => {
    const res = await network(r);
    if (res.url === "") res.url = r.url;
    return res;
  };
  const caches = {
    async open(name: string) {
      if (!stores.has(name)) stores.set(name, new FakeCache(served));
      return stores.get(name)!;
    },
    async keys() {
      return [...stores.keys()];
    },
    async delete(name: string) {
      return stores.delete(name);
    },
  };
  const self = {
    location: { origin: ORIGIN },
    addEventListener(type: string, fn: (event: unknown) => void) {
      (listeners[type] ??= []).push(fn);
    },
    skipWaiting() {
      skipped += 1;
    },
    clients: { claim: () => Promise.resolve() },
  };
  class FakeRequest {
    url: string;
    method = "GET";
    mode = "same-origin";
    destination = "";
    headers = { has: () => false };
    constructor(url: string) {
      this.url = absolute(url);
    }
  }
  const Worker = function (body: string) {
    return response(200, "basic", body);
  } as unknown as { (body: string): Res; redirect(url: string, status?: number): Res };
  // redirect is Response.redirect: the answer's status and its location, as the browser gives them.
  Worker.redirect = (url: string, status = 302) => response(status, "basic", "", { location: url });
  // fetchWaits are the promises the worker keeps the fetch event open with (waitUntil), so a test can see them settle.
  const fetchWaits: Promise<unknown>[] = [];
  runInNewContext(SOURCE, {
    self,
    caches,
    fetch: (r: Req) => served(r),
    Request: FakeRequest,
    Response: Worker,
    URL,
    crypto: globalThis.crypto,
    FormData,
    File,
    setTimeout: (fn: () => void, ms: number) => globalThis.setTimeout(fn, ms),
    clearTimeout: (id: ReturnType<typeof setTimeout>) => globalThis.clearTimeout(id),
  });
  const dispatch = (type: string, event: unknown) => {
    for (const fn of listeners[type] ?? []) fn(event);
  };
  const waitAll = async (fn: (waits: Promise<unknown>[]) => void) => {
    const waits: Promise<unknown>[] = [];
    fn(waits);
    await Promise.all(waits);
  };
  return {
    caches: stores,
    open: (name: string) => caches.open(name),
    skips: () => skipped,
    // fetch returns the worker's answer, or undefined when the worker lets the browser go to the network itself.
    fetch(r: Req): Promise<Res> | undefined {
      let answer: Promise<Res> | undefined;
      dispatch("fetch", {
        request: r,
        respondWith(p: Promise<Res>) {
          answer = p;
        },
        waitUntil(p: Promise<unknown>) {
          fetchWaits.push(p);
        },
      });
      return answer;
    },
    // held is the fetch event's waitUntil promises: a share keeps the worker open until its files are handed over or dropped.
    held: () => fetchWaits,
    install: () => waitAll((w) => dispatch("install", { waitUntil: (p: Promise<unknown>) => w.push(p) })),
    activate: () => waitAll((w) => dispatch("activate", { waitUntil: (p: Promise<unknown>) => w.push(p) })),
    message: (data: unknown, port?: { postMessage(v: unknown): void }) =>
      waitAll((w) =>
        dispatch("message", { data, ports: port ? [port] : [], waitUntil: (p: Promise<unknown>) => w.push(p) }),
      ),
  };
}

describe("the worker's cache rule", () => {
  it("stores a same-origin 200 app file of a build type under /assets/", async () => {
    const w = load();
    const answer = w.fetch(request("/assets/app-abc123.js"));
    expect(answer).toBeDefined();
    await answer;
    expect(w.caches.get(SHELL)?.puts).toEqual([absolute("/assets/app-abc123.js")]);
  });

  it("serves a stored file from the cache without asking the network again", async () => {
    let asked = 0;
    const w = load(async () => {
      asked += 1;
      return response(200, "basic", "first");
    });
    await w.fetch(request("/assets/app.js"));
    const again = await w.fetch(request("/assets/app.js"));
    expect(again?.body).toBe("first");
    expect(asked).toBe(1);
  });

  // Each of these is a request the worker leaves to the browser: no answer of its own, nothing stored.
  it.each([
    ["an API answer", "/api/v1/me"],
    ["the office endpoints", "/wopi/files/abc/contents"],
    ["a share page", "/s/secretlink"],
    ["the share page root", "/s"],
    ["a brand file", "/brand/icon-192.png"],
    ["a file under /assets/ with a query", "/assets/app.js?sig=abc"],
    ["another origin's asset", "https://cdn.example.test/assets/app.js"],
  ])("refuses %s", async (_name, url) => {
    const w = load();
    expect(w.fetch(request(url))).toBeUndefined();
    expect(w.caches.size).toBe(0);
  });

  it("refuses a POST under /assets/", async () => {
    const w = load();
    expect(w.fetch(request("/assets/app.js", { method: "POST" }))).toBeUndefined();
  });

  it("refuses a range request for an app file, and stores nothing", async () => {
    const w = load();
    expect(w.fetch(request("/assets/app.js", { range: true }))).toBeUndefined();
    expect(w.caches.size).toBe(0);
  });

  it.each([
    ["a 404", response(404, "basic")],
    ["a 206 partial answer", response(206, "basic")],
    ["an opaque answer", response(200, "opaque")],
    ["a redirect", response(302, "basic")],
  ])("answers %s without storing it", async (_name, res) => {
    const w = load(async () => res);
    const answer = await w.fetch(request("/assets/app.js"))!;
    expect(answer).toBe(res);
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it("answers a redirected 200 without storing it, even when the redirect ends on a file of the build", async () => {
    const w = load(async () => response(200, "basic", "other", { url: absolute("/assets/other.js"), redirected: true }));
    const answer = await w.fetch(request("/assets/app.js"))!;
    expect(answer.body).toBe("other");
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it("refuses a redirected answer even when the redirect ended on the path that was asked for", async () => {
    const w = load(async (r) => response(200, "basic", "x", { url: r.url, redirected: true }));
    await w.fetch(request("/assets/app.js"));
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it("does not store an answer served from another path than the one asked for", async () => {
    const w = load(async () => response(200, "basic", "x", { url: absolute("/assets/moved.js") }));
    await w.fetch(request("/assets/app.js"));
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it.each([
    ["a double slash", "/assets//app.js"],
    ["an encoded backslash", "/assets/a%5cb.js"],
    ["an encoded slash of a traversal", "/assets/..%2fapi%2fv1%2fme"],
    ["an encoded dot segment", "/assets/%2e%2e/api.js"],
  ])("refuses %s under /assets/", async (_name, path) => {
    const served: string[] = [];
    const w = load(async (r) => {
      served.push(r.url);
      return response(200, "basic", "x", { url: r.url });
    });
    const answer = w.fetch(request(path));
    // The worker answers it through the network, and never stores what came back.
    if (answer) await answer;
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it.each([
    ["text/html", "text/html; charset=utf-8"],
    ["application/json", "application/json"],
    ["no content type", null],
  ])("does not store an answer of the wrong type (%s)", async (_name, contentType) => {
    const w = load(async () => response(200, "basic", "x", { contentType }));
    await w.fetch(request("/assets/app.js"));
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it("stores a stylesheet and a font, which have their own types", async () => {
    const w = load(async () => response(200, "basic", "x", { contentType: "text/css" }));
    await w.fetch(request("/assets/app.css"));
    const f = load(async () => response(200, "basic", "x", { contentType: "font/woff2" }));
    await f.fetch(request("/assets/inter.woff2"));
    expect(w.caches.get(SHELL)?.puts).toEqual([absolute("/assets/app.css")]);
    expect(f.caches.get(SHELL)?.puts).toEqual([absolute("/assets/inter.woff2")]);
  });
});

describe("navigation", () => {
  const doc = (path: string, extra: Partial<Req> = {}) => request(path, { mode: "navigate", destination: "document", ...extra });

  it("goes to the network first and stores nothing from it", async () => {
    const w = load(async () => response(200, "basic", "page", { contentType: "text/html" }));
    await w.install();
    const answer = await w.fetch(doc("/files"))!;
    expect(answer.body).toBe("page");
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it("answers the offline page only when the network fails", async () => {
    let online = true;
    const w = load(async (r) => {
      if (!online && r.mode === "navigate") throw new Error("offline");
      return response(200, "basic", r.url.endsWith("/offline.html") ? "OFFLINE PAGE" : "x");
    });
    await w.install();
    online = false;
    const answer = await w.fetch(doc("/files"))!;
    expect(answer.body).toBe("OFFLINE PAGE");
  });

  it("fails the navigation when there is no offline page to answer with", async () => {
    const w = load(async () => {
      throw new Error("offline");
    });
    await expect(w.fetch(doc("/files"))!).rejects.toThrow("offline");
  });

  it.each([
    ["an iframe's navigation", request("/files", { mode: "navigate", destination: "iframe" })],
    ["a navigation with no document destination", request("/files", { mode: "navigate", destination: "" })],
    ["a download through the API", doc("/api/v1/nodes/n1/download")],
    ["a share page", doc("/s/secretlink")],
    ["the office endpoints", doc("/wopi/files/a/contents")],
    ["a brand file", doc("/brand/logo")],
    ["the manifest", doc("/manifest.webmanifest")],
    ["the share page root", doc("/s")],
  ])("never answers %s with the offline page", async (_name, r) => {
    const w = load(async () => {
      throw new Error("offline");
    });
    await w.install().catch(() => undefined);
    expect(w.fetch(r)).toBeUndefined();
  });
});

describe("install, activate and the messages", () => {
  it("precaches the offline page and the three files it loads at install", async () => {
    const w = load();
    await w.install();
    for (const f of ["/offline.html", "/offline.css", "/offline.js", "/favicon.svg"]) {
      expect(w.caches.get(SHELL)?.entries.has(absolute(f)), f).toBe(true);
    }
  });

  it("answers the offline page's own files from the cache, with the network down", async () => {
    const w = load(async () => {
      throw new Error("offline");
    });
    await w.install().catch(() => undefined);
    const cache = await w.open(SHELL);
    await cache.put("/offline.css", response(200, "basic", "css"));
    const answer = await w.fetch(request("/offline.css", { destination: "style" }))!;
    expect(answer.body).toBe("css");
  });

  it("keeps the current cache and the cache of the build before it, and removes older ones", async () => {
    const w = load();
    await w.open(STATE);
    await (await w.open(STATE)).put("/build", response(200, "basic", "build0"));
    await w.open("selfcloud-build0");
    await w.open("selfcloud-old");
    await w.open("other-app");
    await w.activate();
    expect([...w.caches.keys()].sort()).toEqual([SHELL, STATE, "selfcloud-build0", "other-app"].sort());
  });

  it("removes the cache of two builds back on activate, and records the build", async () => {
    const w = load();
    await (await w.open(STATE)).put("/build", response(200, "basic", "build0"));
    await w.open("selfcloud-build0");
    await w.open("selfcloud-build-2");
    await w.activate();
    expect(w.caches.has("selfcloud-build-2")).toBe(false);
    expect(await (await w.open(STATE)).match("/build").then((r) => r?.body)).toBe("build1");
  });

  it("fetches the offline page again on activate, so a changed brand or file reaches the worker", async () => {
    let fetched = 0;
    const w = load(async (r) => {
      if (r.url.endsWith("/offline.html")) fetched += 1;
      return response(200, "basic", "x", { contentType: "text/html" });
    });
    await w.activate();
    expect(fetched).toBe(1);
    expect((await w.open(SHELL)).entries.has(absolute("/offline.html"))).toBe(true);
  });

  it("clears every app cache, keeps the state, and writes the signed-out marker on CLEAR_CACHES", async () => {
    const w = load();
    await w.install();
    await w.fetch(request("/assets/app.js"));
    await w.message({ type: "CLEAR_CACHES", seq: 1 });
    expect(w.caches.get(SHELL)).toBeUndefined();
    expect(w.caches.get(STATE)?.entries.has(absolute("/signed-out"))).toBe(true);
  });

  it("does not store app files while the signed-out marker is there", async () => {
    const w = load();
    await w.message({ type: "CLEAR_CACHES", seq: 1 });
    await w.fetch(request("/assets/app.js"));
    expect(w.caches.get(SHELL)?.puts ?? []).toEqual([]);
  });

  it("fetches the offline page again on PRECACHE, after sign-out removed it", async () => {
    const w = load();
    await w.install();
    await w.message({ type: "CLEAR_CACHES", seq: 1 });
    expect(w.caches.get(SHELL)).toBeUndefined();
    await w.message({ type: "PRECACHE", seq: 2 });
    expect(w.caches.get(SHELL)?.entries.has(absolute("/offline.html"))).toBe(true);
  });

  it("ignores a CLEAR that is older than the newest PRECACHE, so a late sign-out cannot write the marker back", async () => {
    const w = load();
    await w.message({ type: "PRECACHE", seq: 20 });
    await w.message({ type: "CLEAR_CACHES", seq: 10 });
    expect(w.caches.get(STATE)?.entries.has(absolute("/signed-out")) ?? false).toBe(false);
  });

  it("ignores a PRECACHE that is older than the newest CLEAR", async () => {
    const w = load();
    await w.message({ type: "CLEAR_CACHES", seq: 20 });
    await w.message({ type: "PRECACHE", seq: 10 });
    expect(w.caches.get(SHELL)?.entries.has(absolute("/offline.html")) ?? false).toBe(false);
  });

  it("handles the page's messages one at a time, in the order they arrive", async () => {
    const w = load();
    // A sign-out (seq 5) that arrives before the sign-in (seq 6): the marker is written, then the sign-in lifts it.
    const first = w.message({ type: "CLEAR_CACHES", seq: 5 });
    const second = w.message({ type: "PRECACHE", seq: 6 });
    await Promise.all([first, second]);
    expect(w.caches.get(STATE)?.entries.has(absolute("/signed-out")) ?? false).toBe(false);
    expect(w.caches.get(SHELL)?.entries.has(absolute("/offline.html"))).toBe(true);
  });

  it("a sign-in lifts the marker even when the sign-out's message arrived after it", async () => {
    const w = load();
    await w.message({ type: "CLEAR_CACHES", seq: 5 });
    await w.message({ type: "PRECACHE", seq: 6 });
    expect(w.caches.get(STATE)?.entries.has(absolute("/signed-out")) ?? false).toBe(false);
  });

  it("lets a waiting worker take over on SKIP_WAITING", async () => {
    const w = load();
    await w.message({ type: "SKIP_WAITING" });
    expect(w.skips()).toBe(1);
  });
});

// The share target (decision 82): a form post from the share sheet to /share-target is held in the worker, under a fresh id,
// and handed to the page once, by message. The files are never written to a cache or to storage.
describe("the share target (decision 82)", () => {
  // SIXTY_SECONDS is how long an unclaimed share is kept: no page has taken it, so it expires at once after a minute (decision 82).
  const SIXTY_SECONDS = 60 * 1000;
  const file = (name: string, text: string) => new File([text], name, { type: "text/plain" });
  const idOf = (res: Res) => (res.headers.get("location") ?? "").split("id=")[1] ?? "";
  // flush lets the pending promises run, without a timer (the tests with fake timers must not wait on one).
  const flush = async () => {
    for (let i = 0; i < 10; i++) await Promise.resolve();
  };

  // take asks the worker for a share's files by its id, as the page does, and returns what came back.
  async function take(w: ReturnType<typeof load>, id: string): Promise<File[]> {
    const got: Array<{ files: File[] }> = [];
    await w.message({ type: "SHARE_TAKE", id }, { postMessage: (v: { files: File[] }) => got.push(v) });
    return got[0]?.files ?? [];
  }

  it("answers a share post with 303 to the share page under a fresh random id", async () => {
    const w = load();
    const res = await w.fetch(shareRequest([file("a.txt", "hi")]));
    expect(res?.status).toBe(303);
    // Response.redirect gives the absolute address of the share page, as the browser's Location header does.
    expect(res?.headers.get("location")).toBe(absolute("/share?id=" + idOf(res!)));
    expect(idOf(res!)).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("hands the files over once, by message, and then forgets them", async () => {
    const w = load();
    const id = idOf((await w.fetch(shareRequest([file("a.txt", "hi"), file("b.txt", "yo")])))!);
    expect((await take(w, id)).map((f) => f.name)).toEqual(["a.txt", "b.txt"]);
    expect(await take(w, id)).toEqual([]);
  });

  it("keeps the files out of every cache, so no cache put happens for a share", async () => {
    const w = load();
    const id = idOf((await w.fetch(shareRequest([file("a.txt", "hi")])))!);
    await take(w, id);
    for (const c of w.caches.values()) {
      expect(c.puts).toEqual([]);
      expect(c.entries.size).toBe(0);
    }
  });

  it("two shares are held side by side, each under its own id, and each is handed over on its own", async () => {
    const w = load();
    const first = idOf((await w.fetch(shareRequest([file("one.txt", "1")])))!);
    const second = idOf((await w.fetch(shareRequest([file("two.txt", "2")])))!);
    expect(first).not.toBe(second);
    expect((await take(w, second)).map((f) => f.name)).toEqual(["two.txt"]);
    expect((await take(w, first)).map((f) => f.name)).toEqual(["one.txt"]);
  });

  it("a fifth share drops the oldest unclaimed one, so the newest four are kept", async () => {
    const w = load();
    const ids: string[] = [];
    for (let i = 1; i <= 5; i++) ids.push(idOf((await w.fetch(shareRequest([file(`f${i}.txt`, "x")])))!));
    expect(await take(w, ids[0])).toEqual([]);
    for (let i = 1; i <= 4; i++) expect((await take(w, ids[i])).map((f) => f.name)).toEqual([`f${i + 1}.txt`]);
  });

  it("a cross-site post with no referrer cannot block a later real share", async () => {
    const w = load();
    // A page can send no referrer at all, so this is the post the referrer check cannot see: six of them go in first.
    for (let i = 0; i < 6; i++) {
      await w.fetch(shareRequest([file("attack.txt", "x")], { referrer: "" }));
    }
    const real = idOf((await w.fetch(shareRequest([file("mine.txt", "hi")])))!);
    expect((await take(w, real)).map((f) => f.name)).toEqual(["mine.txt"]);
  });

  it("a share posted into a frame (not a top-level navigation) is left to the network", async () => {
    const w = load();
    expect(w.fetch(shareRequest([file("a.txt", "hi")], { destination: "iframe" }))).toBeUndefined();
    expect(w.held()).toEqual([]);
  });

  it("a second share is free to be held once the first has been handed over", async () => {
    const w = load();
    const first = idOf((await w.fetch(shareRequest([file("a.txt", "1")])))!);
    await take(w, first);
    const second = idOf((await w.fetch(shareRequest([file("b.txt", "2")])))!);
    expect((await take(w, second)).map((f) => f.name)).toEqual(["b.txt"]);
  });

  it("refuses a share whose referrer is another site: 303 to the share page, nothing held", async () => {
    const w = load();
    const res = await w.fetch(shareRequest([file("a.txt", "hi")], { referrer: "https://evil.test/attack.html" }));
    expect(res?.status).toBe(303);
    expect(res?.headers.get("location")).toBe(absolute("/share"));
    let ended = false;
    void Promise.all(w.held()).then(() => (ended = true));
    await flush();
    expect(ended).toBe(true);
    expect(await take(w, "0".repeat(32))).toEqual([]);
  });

  it("refuses a referrer that is not a web address, as another site", async () => {
    const w = load();
    const res = await w.fetch(shareRequest([file("a.txt", "hi")], { referrer: "not a url" }));
    expect(res?.headers.get("location")).toBe(absolute("/share"));
  });

  it("accepts a share with no referrer, as the system share sheet sends it", async () => {
    const w = load();
    const res = await w.fetch(shareRequest([file("a.txt", "hi")], { referrer: "" }));
    expect(res?.headers.get("location")).toMatch(/\/share\?id=[0-9a-f-]{36}$/);
  });

  it("accepts a share from this site's own page, the referrer being this origin", async () => {
    const w = load();
    const res = await w.fetch(shareRequest([file("a.txt", "hi")], { referrer: absolute("/files") }));
    expect(res?.headers.get("location")).toMatch(/\/share\?id=[0-9a-f-]{36}$/);
  });

  it("a hand-over with the wrong id gets nothing and keeps the files for the right id", async () => {
    const w = load();
    const id = idOf((await w.fetch(shareRequest([file("a.txt", "hi")])))!);
    expect(await take(w, "0".repeat(32))).toEqual([]);
    expect((await take(w, id)).map((f) => f.name)).toEqual(["a.txt"]);
  });

  it("an unclaimed share expires after sixty seconds, and its wait ends with it", async () => {
    vi.useFakeTimers();
    try {
      const w = load();
      const id = idOf((await w.fetch(shareRequest([file("a.txt", "hi")])))!);
      let ended = false;
      void Promise.all(w.held()).then(() => (ended = true));
      vi.advanceTimersByTime(SIXTY_SECONDS - 1);
      await flush();
      expect(ended).toBe(false);
      vi.advanceTimersByTime(1);
      await flush();
      expect(ended).toBe(true);
      expect(await take(w, id)).toEqual([]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps the worker open until the files are handed over", async () => {
    const w = load();
    const id = idOf((await w.fetch(shareRequest([file("a.txt", "hi")])))!);
    let ended = false;
    void Promise.all(w.held()).then(() => (ended = true));
    await flush();
    expect(ended).toBe(false);
    await take(w, id);
    await flush();
    expect(ended).toBe(true);
  });

  it("a sign-out drops the held files", async () => {
    const w = load();
    const id = idOf((await w.fetch(shareRequest([file("a.txt", "hi")])))!);
    await w.message({ type: "CLEAR_CACHES", seq: 1 });
    expect(await take(w, id)).toEqual([]);
  });

  it("a share with no files answers 303 to the share page with no id, and holds nothing", async () => {
    const w = load();
    const res = await w.fetch(shareRequest([]));
    expect(res?.status).toBe(303);
    expect(res?.headers.get("location")).toBe(absolute("/share"));
    // Nothing is held, so the worker's wait ends at once.
    let ended = false;
    void Promise.all(w.held()).then(() => (ended = true));
    await flush();
    expect(ended).toBe(true);
    expect(await take(w, "0".repeat(32))).toEqual([]);
  });

  it("leaves every other POST and path to the network", async () => {
    const w = load();
    expect(w.fetch(shareRequest([file("a.txt", "hi")], { url: "/api/v1/upload" }))).toBeUndefined();
    expect(w.fetch(shareRequest([file("a.txt", "hi")], { url: "/share-target/x" }))).toBeUndefined();
    expect(w.fetch(shareRequest([file("a.txt", "hi")], { mode: "same-origin" }))).toBeUndefined();
    expect(w.fetch(shareRequest([file("a.txt", "hi")], { url: "https://other.test/share-target" }))).toBeUndefined();
    expect(w.held()).toEqual([]);
  });
});
