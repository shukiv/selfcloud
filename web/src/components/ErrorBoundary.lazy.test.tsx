// SPDX-License-Identifier: AGPL-3.0-or-later

// The crash view is a lazy chunk: antd's Result and its pictures are not on the first load's path (decision 185).
// This file runs the lazy path: the boundary draws nothing while the chunk is on its way, then the message and the
// reload button. It stays in its own file so the other tests, which may find the chunk already loaded, do not hide it.

import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ErrorBoundary from "./ErrorBoundary";
import { ThemeProvider } from "../theme/theme";

function Broken(): never {
  throw new Error("render failed");
}

afterEach(() => {
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

describe("the crash view loads lazily", () => {
  it("draws nothing before the chunk arrives, then the message and the reload button", async () => {
    // React logs the error it catches; the test only checks the page that is left.
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    render(
      <ThemeProvider>
        <ErrorBoundary>
          <Broken />
        </ErrorBoundary>
      </ThemeProvider>,
    );
    expect(screen.queryByRole("button", { name: /reload/i })).toBeNull();
    expect(await screen.findByRole("button", { name: /reload/i })).toBeTruthy();
    expect(screen.getByText("Something went wrong on this page.")).toBeTruthy();
  });
});
