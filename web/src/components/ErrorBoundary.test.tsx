// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ErrorBoundary from "./ErrorBoundary";
import { ThemeProvider } from "../theme/theme";

function Broken(): never {
  throw new Error("render failed");
}

function renderBoundary() {
  // React logs the error it catches; the test only checks the page that is left.
  vi.spyOn(console, "error").mockImplementation(() => undefined);
  return render(
    <ThemeProvider>
      <ErrorBoundary>
        <Broken />
      </ErrorBoundary>
    </ThemeProvider>,
  );
}

describe("ErrorBoundary", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    window.history.replaceState(null, "", "/");
  });

  it("shows a message and a reload button in place of the failed page", async () => {
    window.history.replaceState(null, "", "/files");
    renderBoundary();
    // The crash view is a lazy chunk (decision 198); the message arrives with it.
    expect(await screen.findByRole("button", { name: /reload/i })).toBeTruthy();
  });

  it("strips the secret fragment from the share-link address when the page fails", async () => {
    window.history.replaceState(null, "", "/s?x=1#" + "a".repeat(43));
    renderBoundary();
    await screen.findByRole("button", { name: /reload/i });
    expect(window.location.pathname).toBe("/s");
    expect(window.location.search).toBe("?x=1");
    expect(window.location.hash).toBe("");
  });

  it("leaves the fragment of any other address alone", async () => {
    window.history.replaceState(null, "", "/files#keep");
    renderBoundary();
    await screen.findByRole("button", { name: /reload/i });
    expect(window.location.hash).toBe("#keep");
  });
});
