// SPDX-License-Identifier: AGPL-3.0-or-later

// The start's read of the signed-in person: only a 401 means signed out. Any other failure — a 5xx, a 429, a network
// error — shows the retry screen instead of the sign-in page, and the boot reads again on its own (1 s, doubling to
// 16 s, then every 30 s), at once on Retry and on the window's online event. The address is never moved, so the page
// opens where the person was. The shell and the files page are stubs, as in App.roles.test.tsx: this file is about the
// boot, not about what they draw.

import { StrictMode } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Boot, Gate } from "./App";
import { createQueryClient } from "./lib/query";
import { session } from "./lib/session";
import { setLang } from "./lib/i18n";
import { stubFetch, fail, type Answer } from "./test/fetchStub";
import { ThemeProvider } from "./theme/theme";

vi.mock("./components/Shell", async () => (await import("./test/stubPage")).stubShell());
vi.mock("./pages/FilesPage", async () => (await import("./test/stubPage")).stubPage("files"));

const ME = {
  user: { id: "usr_m", email: "mateo@example.com", display_name: "Mateo", role: "member", locale: "en", status: "active" },
  account: null,
  csrf_token: "csrf",
  personal_folder_id: "fld_root",
};

// renderBoot mounts the app the way main.tsx does (StrictMode, the query client), at the address, with the signed-in
// read answered by meAnswer for each of its calls in order. The brand read always answers.
function renderBoot(path: string, meAnswer: (call: number) => Answer) {
  let meCalls = 0;
  const { calls } = stubFetch((c) => {
    if (c.url === "/api/v1/info") return { status: 200, body: {} };
    if (c.url === "/api/v1/me") return meAnswer(meCalls++);
    return fail(404, "not_found");
  });
  const router = createMemoryRouter([{ path: "*", element: <Boot><Gate /></Boot> }], { initialEntries: [path] });
  const view = render(
    <StrictMode>
      <QueryClientProvider client={createQueryClient()}>
        <ThemeProvider>
          <RouterProvider router={router} />
        </ThemeProvider>
      </QueryClientProvider>
    </StrictMode>,
  );
  return { router, view, meReads: () => calls.filter((c) => c.url === "/api/v1/me").length };
}

// flush runs the fake clock and lets the fetch and its promise chain settle between the ticks.
async function flush(ms = 0) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

// settle flushes until the query finds its element: a lazy page chunk may need a few rounds. It is bounded, so a
// missing element still fails the test, and it advances no clock, so no retry wait runs out inside it.
async function settle(query: () => unknown) {
  for (let i = 0; i < 20 && query() === null; i++) await flush();
}

let warn: ReturnType<typeof vi.spyOn>;

beforeEach(async () => {
  window.localStorage.clear();
  session.set(null);
  await setLang("en");
  vi.useFakeTimers();
  warn = vi.spyOn(console, "warn").mockImplementation(() => {});
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  session.set(null);
});

describe("the start cannot read the signed-in person", () => {
  it("a 503 shows the retry screen, never the sign-in page, and the 1 s retry opens the page at the same address", async () => {
    const { router, meReads } = renderBoot("/files", (n) => (n === 0 ? fail(503, "unavailable") : { status: 200, body: ME }));
    await flush();
    expect(screen.getByTestId("boot-retry")).toBeTruthy();
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("Can't reach Selfcloud right now")).toBeTruthy();
    expect(screen.getByText("Your session is safe. Trying again…")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
    expect(router.state.location.pathname).toBe("/files");
    expect(screen.queryByText("stub files")).toBeNull();
    expect(meReads()).toBe(1);
    expect(warn).toHaveBeenCalledWith("boot: could not read the signed-in person", 503);
    await flush(999);
    expect(meReads()).toBe(1);
    await flush(1);
    await settle(() => screen.queryByText("stub files"));
    expect(screen.getByText("stub files")).toBeTruthy();
    expect(screen.queryByTestId("boot-retry")).toBeNull();
    expect(router.state.location.pathname).toBe("/files");
    expect(meReads()).toBe(2);
    // Success removes the timer and the listener: nothing more is sent, even on an online event.
    await flush(60_000);
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    await flush();
    expect(meReads()).toBe(2);
  });

  it("a network error shows the retry screen, and the 1 s retry opens the page at the same address", async () => {
    const { router, meReads } = renderBoot("/files", (n) => {
      if (n === 0) throw new Error("the network is down");
      return { status: 200, body: ME };
    });
    await flush();
    expect(screen.getByTestId("boot-retry")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/files");
    expect(meReads()).toBe(1);
    expect(warn).toHaveBeenCalledWith("boot: could not read the signed-in person", "network");
    await flush(1000);
    await settle(() => screen.queryByText("stub files"));
    expect(screen.getByText("stub files")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/files");
    expect(meReads()).toBe(2);
  });

  it("a 429 shows the retry screen, and the 1 s retry opens the page at the same address", async () => {
    const { router, meReads } = renderBoot("/files", (n) => (n === 0 ? fail(429, "rate_limited") : { status: 200, body: ME }));
    await flush();
    expect(screen.getByTestId("boot-retry")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/files");
    expect(meReads()).toBe(1);
    expect(warn).toHaveBeenCalledWith("boot: could not read the signed-in person", 429);
    await flush(1000);
    await settle(() => screen.queryByText("stub files"));
    expect(screen.getByText("stub files")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/files");
    expect(meReads()).toBe(2);
  });

  it("a 401 opens the sign-in page as today, with no retry screen", async () => {
    // No wait is involved here, so the real clock runs: the sign-in page is a lazy chunk, and its words are read with
    // the library's own wait.
    vi.useRealTimers();
    const { router, meReads } = renderBoot("/files", () => fail(401, "unauthorized"));
    expect(await screen.findByLabelText("Email")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toBe("?next=%2Ffiles");
    expect(screen.queryByTestId("boot-retry")).toBeNull();
    expect(meReads()).toBe(1);
    expect(warn).not.toHaveBeenCalledWith("boot: could not read the signed-in person", expect.anything());
  });

  it("Retry sends a read at once, with no wait", async () => {
    const { meReads } = renderBoot("/files", (n) => (n === 0 ? fail(503, "unavailable") : { status: 200, body: ME }));
    await flush();
    expect(meReads()).toBe(1);
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(meReads()).toBe(2);
    await settle(() => screen.queryByText("stub files"));
    expect(screen.getByText("stub files")).toBeTruthy();
  });

  it("the window's online event sends a read at once, with no wait", async () => {
    const { meReads } = renderBoot("/files", (n) => (n === 0 ? fail(503, "unavailable") : { status: 200, body: ME }));
    await flush();
    expect(meReads()).toBe(1);
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    expect(meReads()).toBe(2);
    await settle(() => screen.queryByText("stub files"));
    expect(screen.getByText("stub files")).toBeTruthy();
  });

  it("two quick Retry presses send one read", async () => {
    const { meReads } = renderBoot("/files", () => fail(503, "unavailable"));
    await flush();
    expect(meReads()).toBe(1);
    const retry = screen.getByRole("button", { name: "Retry" });
    fireEvent.click(retry);
    fireEvent.click(retry);
    expect(meReads()).toBe(2);
    await flush();
    expect(meReads()).toBe(2);
  });

  it("the waits grow 1, 2, 4, 8, 16, 30, 30 s", async () => {
    const { meReads } = renderBoot("/files", () => fail(503, "unavailable"));
    await flush();
    let reads = 1;
    expect(meReads()).toBe(reads);
    for (const wait of [1000, 2000, 4000, 8000, 16000, 30000, 30000]) {
      await flush(wait - 1);
      expect(meReads()).toBe(reads);
      await flush(1);
      reads += 1;
      expect(meReads()).toBe(reads);
    }
  });

  it("unmount during a wait sends nothing more", async () => {
    const { view, meReads } = renderBoot("/files", () => fail(503, "unavailable"));
    await flush();
    expect(meReads()).toBe(1);
    view.unmount();
    await flush(60_000);
    act(() => {
      window.dispatchEvent(new Event("online"));
    });
    await flush();
    expect(meReads()).toBe(1);
  });
});
