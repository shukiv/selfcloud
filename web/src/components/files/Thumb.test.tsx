// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Thumb from "./Thumb";

// Observers records the IntersectionObserver each tile makes, so a test can say the tile came into view or left it.
class Observers {
  static list: Observers[] = [];
  disconnected = false;
  constructor(private cb: (entries: Array<{ isIntersecting: boolean }>) => void) {
    Observers.list.push(this);
  }
  observe() {}
  unobserve() {}
  disconnect() {
    this.disconnected = true;
  }
  enter() {
    this.cb([{ isIntersecting: true }]);
  }
  leave() {
    this.cb([{ isIntersecting: false }]);
  }
}

function answer(status: number, headers: Record<string, string> = {}): Response {
  return new Response(status === 200 ? new Uint8Array([0xff, 0xd8, 0xff]) : null, { status, headers });
}

function opaque(): Response {
  return { status: 0, type: "opaqueredirect", headers: new Headers() } as unknown as Response;
}

// flush lets the pending fetch and its promise chain settle.
async function flush(ms = 0) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  Observers.list = [];
  vi.stubGlobal("IntersectionObserver", Observers);
  Object.assign(URL, { createObjectURL: vi.fn(() => "blob:thumb-1"), revokeObjectURL: vi.fn() });
});

afterEach(() => {
  vi.unstubAllGlobals();
  const u = URL as unknown as Record<string, unknown>;
  delete u.createObjectURL;
  delete u.revokeObjectURL;
  vi.useRealTimers();
});

describe("Thumb", () => {
  it("asks for nothing until the tile is in view", async () => {
    const fetchMock = vi.fn(async () => answer(202, { "Retry-After": "1" }));
    vi.stubGlobal("fetch", fetchMock);
    render(<Thumb nod="nod_1" version="v1" fallback={<span>icon</span>} />);
    await flush(5000);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByText("icon")).toBeTruthy();
  });

  it("stops asking when it leaves the page: no request after unmount", async () => {
    const fetchMock = vi.fn(async () => answer(202, { "Retry-After": "1" }));
    vi.stubGlobal("fetch", fetchMock);
    const { unmount } = render(<Thumb nod="nod_1" version="v1" fallback={<span>icon</span>} />);
    act(() => Observers.list[0].enter());
    await flush();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    unmount();
    expect(Observers.list[0].disconnected).toBe(true);
    await flush(10 * 60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stops asking while the tile is scrolled out, and resumes when it comes back", async () => {
    const fetchMock = vi.fn(async () => answer(202, { "Retry-After": "1" }));
    vi.stubGlobal("fetch", fetchMock);
    render(<Thumb nod="nod_1" version="v1" fallback={<span>icon</span>} />);
    act(() => Observers.list[0].enter());
    await flush();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    act(() => Observers.list[0].leave());
    await flush(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    act(() => Observers.list[0].enter());
    await flush();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("asks again when the content changes, so a new version or a restore shows its own picture", async () => {
    const fetchMock = vi.fn(async () => answer(202, { "Retry-After": "1" }));
    vi.stubGlobal("fetch", fetchMock);
    const { rerender } = render(<Thumb nod="nod_1" version="sha-old" fallback={<span>icon</span>} />);
    act(() => Observers.list[0].enter());
    await flush();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    rerender(<Thumb nod="nod_1" version="sha-new" fallback={<span>icon</span>} />);
    act(() => Observers.list[Observers.list.length - 1].enter());
    await flush();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("shows the image once the server has it", async () => {
    const fetchMock = vi.fn(async () => answer(200));
    vi.stubGlobal("fetch", fetchMock);
    const { container } = render(<Thumb nod="nod_1" version="v1" fallback={<span>icon</span>} />);
    act(() => Observers.list[0].enter());
    await flush();
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("blob:thumb-1");
    expect(screen.queryByText("icon")).toBeNull();
  });

  it("keeps the icon, with no console error, when the body cannot be read", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        const res = answer(200);
        res.blob = () => Promise.reject(new TypeError("aborted"));
        return res;
      }),
    );
    const errors = vi.spyOn(console, "error");
    render(<Thumb nod="nod_1" version="v1" fallback={<span>icon</span>} />);
    act(() => Observers.list[0].enter());
    await flush();
    expect(screen.getByText("icon")).toBeTruthy();
    expect(errors).not.toHaveBeenCalled();
    errors.mockRestore();
  });

  it("falls back to the icon when a redirected image fails to load", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => opaque()),
    );
    const { container } = render(<Thumb nod="nod_1" version="v1" fallback={<span>icon</span>} />);
    act(() => Observers.list[0].enter());
    await flush();
    const img = container.querySelector("img");
    expect(img).not.toBeNull();
    fireEvent.error(img as HTMLImageElement);
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText("icon")).toBeTruthy();
  });
});
