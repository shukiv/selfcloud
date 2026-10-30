// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach } from "vitest";
import { cleanup, configure } from "@testing-library/react";

// antd's popups and the role queries are slow on a busy machine; the library's one-second wait would fail tests that
// are only late.
configure({ asyncUtilTimeout: 5000 });

// jsdom has neither of these; antd's responsive grid and resize observers need them.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
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
