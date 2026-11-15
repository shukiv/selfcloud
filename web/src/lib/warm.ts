// SPDX-License-Identifier: AGPL-3.0-or-later

// warmImport starts a lazy chunk's load once the browser is idle, after the first screen is drawn, so the first open
// finds the code already there (decision 185). requestIdleCallback where the browser has it, with a deadline so a busy
// page still warms; a short timeout where it does not. A failed warm is swallowed: the lazy component's own load
// reports the failure where it draws, and the page never breaks for a warm.
export function warmImport(load: () => Promise<unknown>): void {
  const start = () => {
    void load().catch(() => undefined);
  };
  if (typeof window.requestIdleCallback === "function") window.requestIdleCallback(start, { timeout: 2000 });
  else window.setTimeout(start, 200);
}
