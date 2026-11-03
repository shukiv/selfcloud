// SPDX-License-Identifier: AGPL-3.0-or-later

// The page's side of the worker (decision 65): an update is applied only when no tab is busy, the tabs reload when the new
// version takes over, one listener serves the page, and the app checks for updates while it stays open.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const others = vi.hoisted(() => ({ busy: false }));
vi.mock("./tabs", async (importOriginal) => {
  const real = await importOriginal<typeof import("./tabs")>();
  return { ...real, askOtherTabsBusy: async () => others.busy };
});

import { busy, applyUpdate, startUpdateChecks, waiting } from "./worker";
import { uploads, type UploadItem } from "../files/uploads";
import { get } from "../store";

function item(status: UploadItem["status"]): UploadItem {
  return { id: "u1", status } as unknown as UploadItem;
}

function fakeWorker(state = "installed") {
  return { state, postMessage: vi.fn() } as unknown as ServiceWorker & { postMessage: ReturnType<typeof vi.fn> };
}

beforeEach(() => {
  uploads.set([]);
  waiting.set(null);
  others.busy = false;
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  delete (navigator as unknown as { serviceWorker?: unknown }).serviceWorker;
});

describe("busy", () => {
  it("is false when no upload is open", () => {
    expect(busy()).toBe(false);
    uploads.set([item("done"), item("failed"), item("cancelled")]);
    expect(busy()).toBe(false);
  });

  it.each(["queued", "uploading", "paused", "waiting", "conflict", "awaiting_name"] as const)("is true while an upload is %s", (status) => {
    uploads.set([item(status)]);
    expect(busy()).toBe(true);
  });
});

describe("applyUpdate", () => {
  it("does nothing when no new version waits", async () => {
    expect(await applyUpdate()).toBe("none");
  });

  it("refuses while this tab has an upload under way, and tells no worker to take over", async () => {
    const next = fakeWorker();
    waiting.set(next);
    uploads.set([item("uploading")]);
    expect(await applyUpdate()).toBe("busy");
    expect(next.postMessage).not.toHaveBeenCalled();
  });

  it("refuses while another tab is busy, and says so", async () => {
    const next = fakeWorker();
    waiting.set(next);
    others.busy = true;
    expect(await applyUpdate()).toBe("otherTabs");
    expect(next.postMessage).not.toHaveBeenCalled();
  });

  it("tells the waiting worker to take over when no tab is busy", async () => {
    const next = fakeWorker();
    waiting.set(next);
    expect(await applyUpdate()).toBe("applied");
    expect(next.postMessage).toHaveBeenCalledWith({ type: "SKIP_WAITING" });
  });

  it("does not throw on a worker that was replaced while it waited, and reports it", async () => {
    const next = fakeWorker("redundant");
    waiting.set(next);
    await expect(applyUpdate()).resolves.toBe("failed");
    expect(next.postMessage).not.toHaveBeenCalled();
    expect(get(waiting)).toBeNull();
  });

  it("reports a worker that refuses the message, instead of throwing", async () => {
    const next = fakeWorker();
    next.postMessage.mockImplementation(() => {
      throw new Error("InvalidStateError");
    });
    waiting.set(next);
    await expect(applyUpdate()).resolves.toBe("failed");
  });
});

describe("registerWorker", () => {
  it("adds one controllerchange listener for the page, however often it is called", async () => {
    const listen = vi.fn();
    const reg = { waiting: null, addEventListener: vi.fn(), update: vi.fn(async () => undefined) };
    Object.defineProperty(navigator, "serviceWorker", {
      configurable: true,
      value: { controller: {}, addEventListener: listen, register: vi.fn(async () => reg) },
    });
    Object.defineProperty(window, "isSecureContext", { configurable: true, value: true });
    vi.resetModules();
    const fresh = await import("./worker");
    fresh.registerWorker();
    fresh.registerWorker();
    await new Promise((r) => setTimeout(r, 0));
    expect(listen.mock.calls.filter(([type]) => type === "controllerchange")).toHaveLength(1);
  });

  // The worker claims the open page when it is first installed (sw.js activate). That page already runs the current version,
  // and a reload then cleared a sign-in being typed (gate fu1b: the e2e sign-in found its field gone). Only a page that a
  // worker controlled before reloads, since that one runs the old version.
  async function changeOf(controller: object | null) {
    const handlers: Array<() => void> = [];
    const reg = { waiting: null, addEventListener: vi.fn(), update: vi.fn(async () => undefined) };
    Object.defineProperty(navigator, "serviceWorker", {
      configurable: true,
      value: {
        controller,
        addEventListener: (type: string, fn: () => void) => type === "controllerchange" && handlers.push(fn),
        register: vi.fn(async () => reg),
      },
    });
    Object.defineProperty(window, "isSecureContext", { configurable: true, value: true });
    vi.resetModules();
    const fresh = await import("./worker");
    const reload = vi.fn();
    fresh.registerWorker(reload);
    return { take: () => handlers.forEach((fn) => fn()), reload };
  }

  it("does not reload a page that no worker controlled when the first worker takes it", async () => {
    const { take, reload } = await changeOf(null);
    take();
    expect(reload).not.toHaveBeenCalled();
  });

  it("reloads a page that a worker controlled when a new worker takes over", async () => {
    const { take, reload } = await changeOf({});
    take();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("reloads on the next change after the first worker took an uncontrolled page", async () => {
    const { take, reload } = await changeOf(null);
    take();
    take();
    expect(reload).toHaveBeenCalledTimes(1);
  });
});

describe("reloadWhenIdle", () => {
  it("reloads at once when the tab is idle, and only once", async () => {
    vi.resetModules();
    const { reloadWhenIdle: fresh } = await import("./worker");
    const reload = vi.fn();
    fresh(reload);
    fresh(reload);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("waits for the upload to end, then reloads once", async () => {
    vi.useFakeTimers();
    vi.resetModules();
    const { reloadWhenIdle: fresh } = await import("./worker");
    const { uploads: store } = await import("../files/uploads");
    store.set([item("uploading")]);
    const reload = vi.fn();
    fresh(reload);
    fresh(reload);
    vi.advanceTimersByTime(3000);
    expect(reload).not.toHaveBeenCalled();
    store.set([item("done")]);
    vi.advanceTimersByTime(1100);
    vi.advanceTimersByTime(3000);
    expect(reload).toHaveBeenCalledTimes(1);
  });
});

describe("startUpdateChecks", () => {
  it("asks for a new worker when the page becomes visible, and every hour, and stops when told", () => {
    vi.useFakeTimers();
    const update = vi.fn(async () => undefined);
    const doc = document.implementation.createHTMLDocument("t");
    let visibility = "hidden";
    Object.defineProperty(doc, "visibilityState", { configurable: true, get: () => visibility });
    const stop = startUpdateChecks({ update }, doc, 3600000);
    doc.dispatchEvent(new Event("visibilitychange"));
    expect(update).not.toHaveBeenCalled();
    visibility = "visible";
    doc.dispatchEvent(new Event("visibilitychange"));
    expect(update).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(3600000);
    expect(update).toHaveBeenCalledTimes(2);
    stop();
    vi.advanceTimersByTime(3600000 * 2);
    doc.dispatchEvent(new Event("visibilitychange"));
    expect(update).toHaveBeenCalledTimes(2);
  });
});
