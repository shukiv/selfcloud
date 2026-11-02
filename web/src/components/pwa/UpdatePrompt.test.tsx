// SPDX-License-Identifier: AGPL-3.0-or-later

// The update offer (decision 65) appears when a new worker waits and the page is idle, is withdrawn while a file plays or an
// upload is under way, and is closed by its close button until the next new version.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ busy: false, applied: 0, result: "applied" as string }));
vi.mock("../../lib/pwa/worker", async () => {
  const { writable } = await import("../../lib/store");
  return {
    waiting: writable<ServiceWorker | null>(null),
    busy: () => state.busy,
    applyUpdate: async () => {
      state.applied += 1;
      return state.result;
    },
  };
});

import UpdatePrompt from "./UpdatePrompt";
import { waiting } from "../../lib/pwa/worker";

const TEXT = "A new version is ready";

beforeEach(() => {
  vi.useFakeTimers();
  state.busy = false;
  state.applied = 0;
  state.result = "applied";
  waiting.set(null);
});

afterEach(() => {
  vi.useRealTimers();
});

describe("UpdatePrompt", () => {
  it("offers the new version when it waits and the page is idle, with Reload", () => {
    render(<UpdatePrompt />);
    act(() => waiting.set({} as ServiceWorker));
    expect(screen.getByText(TEXT)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(state.applied).toBe(1);
  });

  it("is not shown while the page is busy, and appears when it goes idle", () => {
    state.busy = true;
    render(<UpdatePrompt />);
    act(() => waiting.set({} as ServiceWorker));
    expect(screen.queryByText(TEXT)).toBeNull();
    state.busy = false;
    act(() => {
      vi.advanceTimersByTime(1100);
    });
    expect(screen.getByText(TEXT)).toBeTruthy();
  });

  it("is taken away when a file starts playing or an upload starts", () => {
    render(<UpdatePrompt />);
    act(() => waiting.set({} as ServiceWorker));
    expect(screen.getByText(TEXT)).toBeTruthy();
    state.busy = true;
    act(() => {
      vi.advanceTimersByTime(1100);
    });
    expect(screen.queryByText(TEXT)).toBeNull();
  });

  it("stays closed once closed, and is offered again for a new version", () => {
    render(<UpdatePrompt />);
    act(() => waiting.set({} as ServiceWorker));
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByText(TEXT)).toBeNull();
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.queryByText(TEXT)).toBeNull();
    act(() => waiting.set({} as ServiceWorker));
    expect(screen.getByText(TEXT)).toBeTruthy();
  });

  it("says why Reload did not apply the update when another tab is busy", async () => {
    state.result = "otherTabs";
    render(<UpdatePrompt />);
    act(() => waiting.set({} as ServiceWorker));
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByText("Waiting for other tabs to finish. Press Reload again when they are done.")).toBeTruthy();
  });

  it("says when the worker could not take over", async () => {
    state.result = "failed";
    render(<UpdatePrompt />);
    act(() => waiting.set({} as ServiceWorker));
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByText("The update could not be applied. Reload the page to try again.")).toBeTruthy();
  });
});
