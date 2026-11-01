// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach } from "vitest";
import { cleanup, configure } from "@testing-library/react";
import { loadLang } from "../lib/i18n";

// English is loaded before a test runs, as main.tsx loads it before the first render.
await loadLang("en");

// antd's popups and the role queries are slow on a busy machine; the library's one-second wait would fail tests that
// are only late.
configure({ asyncUtilTimeout: 5000 });

// jsdom has neither of these; antd's responsive grid and resize observers need them.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  // A desktop window of a roomy width: the selection panel keeps all its icons (the 360 and 1000 pixel queries match).
  value: (query: string) => ({
    matches: query === "(min-width: 1000px)" || query === "(min-width: 360px)",
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;

// Each test starts with an empty document: the testing library does not clean up on its own
// unless the test globals are on.
afterEach(() => {
  cleanup();
});
