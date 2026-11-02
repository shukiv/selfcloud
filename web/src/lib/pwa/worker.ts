// SPDX-License-Identifier: AGPL-3.0-or-later

// The page's side of the service worker (public/sw.js, decision 65): registering it, offering a new version, applying it only
// when no tab of this browser is busy, reloading the tabs that the new version reaches, and checking for updates while the app
// stays open. Nothing here reads or writes a person's data.

import { writable, get } from "../store";
import { isBusy } from "./busy";
import { askOtherTabsBusy, newTabId, startTab } from "./tabs";

// WORKER_URL is the worker's address. It sits at the root, so its scope is the whole app.
export const WORKER_URL = "/sw.js";
// HOUR is how often an open app asks the server for a new worker, besides each time the page becomes visible.
const HOUR = 60 * 60 * 1000;
// POLL_MS is how often a tab waiting to reload checks whether it is still busy.
const POLL_MS = 1000;

// waiting is the new worker that waits for the page to let it take over, or null.
export const waiting = writable<ServiceWorker | null>(null);

// ApplyResult says what applyUpdate did: none (no new version), busy (this tab is busy), otherTabs (another tab is busy),
// failed (the worker could not take over), or applied (it was told to; this tab reloads when it takes over).
export type ApplyResult = "none" | "busy" | "otherTabs" | "failed" | "applied";

const tabId = newTabId();
let registered = false;
let reloading = false;
let pollTimer: number | undefined;

// busy reports whether a reload now would cut something off in this tab.
export function busy(): boolean {
  return isBusy();
}

// registerWorker registers the worker once the page has loaded, so it stays out of the first load, and makes this tab answer
// the other tabs' questions about updates. Where workers are not available (a plain http address, or an old browser) the app
// works without it.
export function registerWorker(): void {
  if (registered || !("serviceWorker" in navigator) || !window.isSecureContext) return;
  registered = true;
  startTab(tabId, isBusy);
  // One listener for the page: when a new worker takes over, this tab reloads, now or when it is idle.
  navigator.serviceWorker.addEventListener("controllerchange", () => reloadWhenIdle());
  const start = () => {
    navigator.serviceWorker
      .register(WORKER_URL)
      .then(track)
      .catch(() => undefined);
  };
  if (document.readyState === "complete") start();
  else window.addEventListener("load", start, { once: true });
}

// track offers the waiting worker, and keeps checking for updates. A worker installed while an older one controls the page is
// an update.
function track(reg: ServiceWorkerRegistration): void {
  if (reg.waiting && navigator.serviceWorker.controller) waiting.set(reg.waiting);
  reg.addEventListener("updatefound", () => {
    const next = reg.installing;
    if (!next) return;
    next.addEventListener("statechange", () => {
      if (next.state === "installed" && navigator.serviceWorker.controller) waiting.set(next);
    });
  });
  startUpdateChecks(reg);
}

// startUpdateChecks asks the server for a new worker each time the page becomes visible, and every hour. It returns a function
// that stops the checks.
export function startUpdateChecks(
  reg: { update(): Promise<unknown> },
  doc: Document = document,
  everyMs = HOUR,
): () => void {
  const check = () => {
    reg.update().catch(() => undefined);
  };
  const onVisible = () => {
    if (doc.visibilityState === "visible") check();
  };
  doc.addEventListener("visibilitychange", onVisible);
  const timer = window.setInterval(check, everyMs);
  return () => {
    doc.removeEventListener("visibilitychange", onVisible);
    window.clearInterval(timer);
  };
}

// reloadWhenIdle reloads this tab once it is idle, and only once. A tab that is busy waits for its file or upload to end.
export function reloadWhenIdle(reload: () => void = () => window.location.reload()): void {
  if (reloading) return;
  if (!isBusy()) {
    reloading = true;
    reload();
    return;
  }
  if (pollTimer !== undefined) return;
  pollTimer = window.setInterval(() => {
    if (isBusy()) return;
    window.clearInterval(pollTimer);
    pollTimer = undefined;
    reloading = true;
    reload();
  }, POLL_MS);
}

// applyUpdate lets the waiting worker take over, when no tab of this browser is busy. The other tabs reload when it does.
export async function applyUpdate(): Promise<ApplyResult> {
  const next = get(waiting);
  if (!next) return "none";
  if (next.state !== "installed") {
    // The worker was replaced or dropped while it waited: nothing to take over.
    waiting.set(null);
    return "failed";
  }
  if (isBusy()) return "busy";
  if (await askOtherTabsBusy(tabId, 300)) return "otherTabs";
  try {
    next.postMessage({ type: "SKIP_WAITING" });
    return "applied";
  } catch {
    return "failed";
  }
}
