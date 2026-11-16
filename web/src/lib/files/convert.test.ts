// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../apiError";
import {
  exportFormatsFor,
  failureOf,
  previewStep,
  startExport,
  watchPreview,
  MAX_CONVERT_WAITS,
  type ConvertHandlers,
  type ExportDeps,
  type PreviewDeps,
} from "./convert";

// fakeRes is the part of a Response the watchers read.
function fakeRes(status: number, extra: { type?: string; retryAfter?: string; reason?: string } = {}) {
  const headers = new Map<string, string>();
  if (extra.retryAfter) headers.set("Retry-After", extra.retryAfter);
  const cancel = vi.fn();
  return {
    status,
    type: extra.type ?? "basic",
    headers: { get: (k: string) => headers.get(k) ?? null },
    body: { cancel },
    json: async () => (extra.reason ? { error: { code: "unsupported_media_type", details: { reason: extra.reason } } } : {}),
    cancel,
  };
}

// scripted returns a fetch that answers each call with the next item, then repeats the last one.
function scripted(items: Array<ReturnType<typeof fakeRes> | Error>) {
  const calls: Array<{ url: string; method: string }> = [];
  let i = 0;
  const fn = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? "GET" });
    const next = items[Math.min(i, items.length - 1)];
    i++;
    if (next instanceof Error) throw next;
    return next;
  });
  return { fn, calls };
}

// timers runs a scheduled step at once, so the watch runs through its answers without real waits.
function immediate() {
  const pending: Array<() => void> = [];
  return {
    schedule: (fn: () => void) => {
      pending.push(fn);
      return pending.length;
    },
    cancel: vi.fn(),
    // step runs the next scheduled call, if any.
    step: () => {
      const fn = pending.shift();
      fn?.();
      return fn !== undefined;
    },
  };
}

// settle lets the awaited answers of a watch run, then runs what was scheduled, until nothing is left or the bound is hit.
async function run(t: ReturnType<typeof immediate>, rounds = 500) {
  for (let i = 0; i < rounds; i++) {
    await new Promise((r) => setTimeout(r, 0));
    if (!t.step()) {
      await new Promise((r) => setTimeout(r, 0));
      if (!t.step()) return;
    }
  }
}

function handlers() {
  const h = {
    making: vi.fn(),
    ready: vi.fn(),
    failed: vi.fn(),
    slow: vi.fn(),
  };
  return h;
}

// Each test wires the handlers this way, so a watch is typed as the page uses it.
function asHandlers(h: ReturnType<typeof handlers>): ConvertHandlers {
  return { making: h.making, ready: h.ready, failed: h.failed, slow: h.slow };
}

describe("failureOf", () => {
  it("reads a refusal by its status and reason, never by text", () => {
    expect(failureOf(403)).toBe("restricted");
    expect(failureOf(413)).toBe("tooLarge");
    expect(failureOf(415, "not_convertible")).toBe("notConvertible");
    expect(failureOf(415, "failed")).toBe("failed");
    // A HEAD carries no body, so a 415 there is a failed conversion: the only 415 a read gets after the first.
    expect(failureOf(415)).toBe("failed");
    expect(failureOf(503)).toBe("unavailable");
    expect(failureOf(429)).toBe("unavailable");
    expect(failureOf(404)).toBe("failed");
    expect(failureOf(500)).toBe("failed");
  });
});

describe("previewStep", () => {
  it("reads ready, pending and failed answers", () => {
    expect(previewStep(fakeRes(200))).toEqual({ kind: "ready" });
    expect(previewStep(fakeRes(0, { type: "opaqueredirect" }))).toEqual({ kind: "ready" });
    expect(previewStep({ status: 202, retryAfter: "3" })).toEqual({ kind: "wait", ms: 3000 });
    expect(previewStep({ status: 415, reason: "failed" })).toEqual({ kind: "failed", failure: "failed" });
  });
});

describe("exportFormatsFor", () => {
  it("offers the formats of the same family, and PDF", () => {
    expect(exportFormatsFor("letter.docx")).toEqual(["pdf", "docx", "odt"]);
    expect(exportFormatsFor("sheet.xlsx")).toEqual(["pdf", "xlsx", "ods"]);
    expect(exportFormatsFor("deck.PPTX")).toEqual(["pdf", "pptx", "odp"]);
    expect(exportFormatsFor("letter.odt")).toEqual(["pdf", "odt", "docx"]);
    // A legacy binary format is only offered as PDF: its other conversions are not known to work.
    expect(exportFormatsFor("old.doc")).toEqual(["pdf"]);
    expect(exportFormatsFor("notes.txt")).toEqual([]);
  });
});

describe("watchPreview", () => {
  const nod = "nod_1";

  it("queues on the first GET, polls with HEAD, and is ready when HEAD says so", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(202, { retryAfter: "1" }), fakeRes(202), fakeRes(200)]);
    const deps: PreviewDeps = { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" };
    watchPreview(nod, asHandlers(h), deps);
    await run(t);
    expect(h.making).toHaveBeenCalled();
    expect(h.ready).toHaveBeenCalledTimes(1);
    expect(h.ready.mock.calls[0][0]).toBe(`/api/v1/nodes/${nod}/preview?format=pdf`);
    expect(s.calls.map((c) => c.method)).toEqual(["GET", "HEAD", "HEAD"]);
    expect(h.failed).not.toHaveBeenCalled();
  });

  it("is ready at once when the first answer is a 303 to the content origin, and never follows it", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(0, { type: "opaqueredirect" })]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.ready).toHaveBeenCalledTimes(1);
    expect(s.fn).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ redirect: "manual" }));
    expect(s.calls).toHaveLength(1);
  });

  it("cancels the body of a 200 it does not need", async () => {
    const t = immediate();
    const h = handlers();
    const answer = fakeRes(200);
    const s = scripted([answer]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.ready).toHaveBeenCalledTimes(1);
    expect(answer.body.cancel).toHaveBeenCalled();
  });

  it("shows a failed conversion at once, from the first answer, with its reason", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(415, { reason: "failed" })]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("failed");
    expect(h.ready).not.toHaveBeenCalled();
    expect(s.calls).toHaveLength(1);
  });

  it("says a type that cannot be converted is not convertible", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(415, { reason: "not_convertible" })]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("notConvertible");
  });

  it("says the converter is unavailable on a 503, and does not spin", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(503, { retryAfter: "60" })]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("unavailable");
    expect(s.calls).toHaveLength(1);
  });

  it("stops at once with the restricted state on a 403", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(403)]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("restricted");
  });

  it("reports a failed conversion found by a HEAD, the reason a repeat read gives", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(202), fakeRes(415)]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("failed");
    expect(h.ready).not.toHaveBeenCalled();
  });

  it("treats a network failure as a failed conversion, once", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([new TypeError("offline")]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t);
    expect(h.failed).toHaveBeenCalledTimes(1);
    expect(h.failed).toHaveBeenCalledWith("failed");
  });

  it("gives up after its waits, and says the conversion is slow, not that it is still going", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(202)]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    await run(t, 5000);
    expect(h.slow).toHaveBeenCalledTimes(1);
    expect(s.calls.length).toBe(MAX_CONVERT_WAITS + 1);
  });

  it("a stopped watch asks no more and reports nothing", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(202), fakeRes(200)]);
    const stop = watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "person-1\ncsrf-1" });
    stop();
    await run(t);
    expect(s.fn).toHaveBeenCalledTimes(0);
    expect(h.ready).not.toHaveBeenCalled();
    expect(h.failed).not.toHaveBeenCalled();
  });

  it("sends no first GET after the session ended, and reports nothing", async () => {
    let key = "person-1\ncsrf-1";
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(200)]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => key });
    // The sign-out lands while the first GET is still scheduled.
    key = "";
    await run(t);
    expect(s.fn).not.toHaveBeenCalled();
    expect(h.making).not.toHaveBeenCalled();
    expect(h.ready).not.toHaveBeenCalled();
    expect(h.failed).not.toHaveBeenCalled();
    expect(h.slow).not.toHaveBeenCalled();
  });

  it("sends no HEAD whose wait was scheduled before the session ended, and reports nothing", async () => {
    let key = "person-1\ncsrf-1";
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(202, { retryAfter: "1" }), fakeRes(200)]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => key });
    // The first GET runs, and its 202 schedules the HEAD's wait.
    t.step();
    await new Promise((r) => setTimeout(r, 0));
    expect(s.calls.map((c) => c.method)).toEqual(["GET"]);
    // The sign-out lands while the HEAD's wait is scheduled.
    key = "";
    await run(t);
    expect(s.calls.map((c) => c.method)).toEqual(["GET"]);
    expect(h.ready).not.toHaveBeenCalled();
    expect(h.failed).not.toHaveBeenCalled();
    expect(h.slow).not.toHaveBeenCalled();
  });

  it("does not point the frame at the file when the session ended before the HEAD's 200 was read", async () => {
    let key = "person-1\ncsrf-1";
    const t = immediate();
    const h = handlers();
    const fn = vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === "HEAD") {
        // The sign-out lands while the HEAD is on its way: its 200 arrives under a session that ended.
        key = "";
        return fakeRes(200);
      }
      return fakeRes(202, { retryAfter: "1" });
    });
    watchPreview(nod, asHandlers(h), { fetch: fn as unknown as typeof fetch, ...t, sessionKey: () => key });
    await run(t);
    expect(fn).toHaveBeenCalledTimes(2);
    expect(h.ready).not.toHaveBeenCalled();
    expect(h.failed).not.toHaveBeenCalled();
    expect(h.slow).not.toHaveBeenCalled();
  });

  it("a watch with no session keeps its empty key and is ready as usual", async () => {
    const t = immediate();
    const h = handlers();
    const s = scripted([fakeRes(202, { retryAfter: "1" }), fakeRes(200)]);
    watchPreview(nod, asHandlers(h), { fetch: s.fn as unknown as typeof fetch, ...t, sessionKey: () => "" });
    await run(t);
    expect(h.ready).toHaveBeenCalledTimes(1);
    expect(s.calls.map((c) => c.method)).toEqual(["GET", "HEAD"]);
    expect(h.failed).not.toHaveBeenCalled();
  });
});

describe("startExport", () => {
  const nod = "nod_1";

  function exportDeps(
    post: ExportDeps["post"],
    head: ExportDeps["head"],
    t: ReturnType<typeof immediate>,
    keyOf: () => string = () => "person-1\ncsrf-1",
  ): ExportDeps {
    return { post, head, ...t, sessionKey: keyOf };
  }

  it("queues, polls, and is ready with the export's address", async () => {
    const t = immediate();
    const h = handlers();
    const head = vi
      .fn<ExportDeps["head"]>()
      .mockResolvedValueOnce({ status: 202, retryAfter: 1 })
      .mockResolvedValueOnce({ status: 200 });
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t));
    await run(t);
    expect(h.ready).toHaveBeenCalledWith(`/api/v1/nodes/${nod}/exports/drv_1`);
    expect(head).toHaveBeenCalledWith(nod, "drv_1");
  });

  it("shows a refused queueing with its reason", async () => {
    const t = immediate();
    const h = handlers();
    const err = new ApiError(415, "unsupported_media_type", { reason: "not_convertible" }, 0);
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => Promise.reject(err), vi.fn(), t));
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("notConvertible");
  });

  it("shows the failure of a conversion on the first read of its ID, not 404", async () => {
    const t = immediate();
    const h = handlers();
    const head = vi.fn<ExportDeps["head"]>().mockResolvedValueOnce({ status: 415 });
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t));
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("failed");
    expect(h.ready).not.toHaveBeenCalled();
  });

  it("shows a missing row and a converter outage as their own failures", async () => {
    const t = immediate();
    const h = handlers();
    const head = vi.fn<ExportDeps["head"]>().mockResolvedValueOnce({ status: 503, retryAfter: 60 });
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t));
    await run(t);
    expect(h.failed).toHaveBeenCalledWith("unavailable");
  });

  it("gives up as slow after its waits", async () => {
    const t = immediate();
    const h = handlers();
    const head = vi.fn<ExportDeps["head"]>().mockResolvedValue({ status: 202 });
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t));
    await run(t, 5000);
    expect(h.slow).toHaveBeenCalledTimes(1);
  });

  it("a stopped export asks no more", async () => {
    const t = immediate();
    const h = handlers();
    const head = vi.fn<ExportDeps["head"]>().mockResolvedValue({ status: 202 });
    const stop = startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t));
    stop();
    await run(t);
    expect(head).not.toHaveBeenCalled();
    expect(h.ready).not.toHaveBeenCalled();
  });

  it("queues nothing after the session ended, and reports nothing", async () => {
    let key = "person-1\ncsrf-1";
    const t = immediate();
    const h = handlers();
    const post = vi.fn(async () => ({ id: "drv_1" }));
    const head = vi.fn<ExportDeps["head"]>().mockResolvedValue({ status: 200 });
    startExport(nod, "odt", asHandlers(h), exportDeps(post, head, t, () => key));
    // The sign-out lands while the queueing is still scheduled.
    key = "";
    await run(t);
    expect(post).not.toHaveBeenCalled();
    expect(head).not.toHaveBeenCalled();
    expect(h.making).not.toHaveBeenCalled();
    expect(h.ready).not.toHaveBeenCalled();
    expect(h.failed).not.toHaveBeenCalled();
    expect(h.slow).not.toHaveBeenCalled();
  });

  it("sends no further poll after the session ended between two polls, and reports nothing", async () => {
    let key = "person-1\ncsrf-1";
    const t = immediate();
    const h = handlers();
    const head = vi.fn<ExportDeps["head"]>().mockResolvedValue({ status: 202, retryAfter: 1 });
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t, () => key));
    // The queueing runs, its answer starts the first poll, and the first poll's 202 schedules the next wait.
    t.step();
    await new Promise((r) => setTimeout(r, 0));
    expect(head).toHaveBeenCalledTimes(1);
    // The sign-out lands while the next poll's wait is scheduled.
    key = "";
    await run(t);
    expect(head).toHaveBeenCalledTimes(1);
    expect(h.ready).not.toHaveBeenCalled();
    expect(h.failed).not.toHaveBeenCalled();
    expect(h.slow).not.toHaveBeenCalled();
  });

  it("does not start the download when the session ended before the poll's 200 was read", async () => {
    let key = "person-1\ncsrf-1";
    const t = immediate();
    const h = handlers();
    const head = vi.fn<ExportDeps["head"]>(async () => {
      // The sign-out lands while the poll is on its way: its 200 arrives under a session that ended.
      key = "";
      return { status: 200 };
    });
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t, () => key));
    await run(t);
    expect(head).toHaveBeenCalledTimes(1);
    expect(h.ready).not.toHaveBeenCalled();
    expect(h.failed).not.toHaveBeenCalled();
    expect(h.slow).not.toHaveBeenCalled();
  });

  it("an export with no session keeps its empty key and is ready as usual", async () => {
    const t = immediate();
    const h = handlers();
    const head = vi
      .fn<ExportDeps["head"]>()
      .mockResolvedValueOnce({ status: 202, retryAfter: 1 })
      .mockResolvedValueOnce({ status: 200 });
    startExport(nod, "odt", asHandlers(h), exportDeps(async () => ({ id: "drv_1" }), head, t, () => ""));
    await run(t);
    expect(h.ready).toHaveBeenCalledWith(`/api/v1/nodes/${nod}/exports/drv_1`);
    expect(h.failed).not.toHaveBeenCalled();
  });
});
