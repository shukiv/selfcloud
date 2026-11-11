// SPDX-License-Identifier: AGPL-3.0-or-later

// The sentinel asks for the next page once each time its element comes into view, and never while a page loads (design 2.2, paging).
import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLoadMoreSentinel } from "./loadMoreSentinel";

type Fire = (isIntersecting: boolean) => void;
let observers: Fire[] = [];

// A fake observer delivers nothing once it is disconnected, as a real one does, so a test can tell a dead observer from a live one.
class FakeObserver {
  private live = true;
  constructor(private readonly cb: (entries: { isIntersecting: boolean }[]) => void) {
    observers.push((isIntersecting) => {
      if (this.live) this.cb([{ isIntersecting }]);
    });
  }
  observe() {}
  disconnect() {
    this.live = false;
  }
  unobserve() {}
  takeRecords() {
    return [];
  }
}

function Probe(props: { enabled: boolean; loading: boolean; failed?: boolean; more: () => void }) {
  const ref = useLoadMoreSentinel(props.enabled, props.loading, props.failed ?? false, props.more);
  return <div ref={ref} data-testid="sentinel" />;
}

beforeEach(() => {
  observers = [];
  vi.stubGlobal("IntersectionObserver", FakeObserver);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useLoadMoreSentinel", () => {
  it("asks for the next page once when the sentinel comes into view", () => {
    const more = vi.fn();
    render(<Probe enabled loading={false} more={more} />);
    observers.at(-1)?.(true);
    expect(more).toHaveBeenCalledTimes(1);
  });

  it("does not watch the sentinel while a page is loading, so it asks nothing", () => {
    const more = vi.fn();
    render(<Probe enabled loading more={more} />);
    expect(observers).toHaveLength(0);
    expect(more).not.toHaveBeenCalled();
  });

  it("watches again once a load succeeds, so a sentinel still in view asks for the next page", () => {
    const more = vi.fn();
    const { rerender } = render(<Probe enabled loading more={more} />);
    rerender(<Probe enabled loading={false} more={more} />);
    observers.at(-1)?.(true);
    expect(more).toHaveBeenCalledTimes(1);
  });

  // A failed next page must not be asked for again by the sentinel. Its observer would fire at once, since the
  // sentinel is still in view, and the request would fail and repeat with no delay. Only Load more tries again, and a later
  // successful load re-arms the sentinel.
  it("after a failed next page, asks nothing on its own: no observer is made until a load succeeds", () => {
    const more = vi.fn();
    const { rerender } = render(<Probe enabled loading={false} more={more} />);
    rerender(<Probe enabled loading more={more} />);
    rerender(<Probe enabled loading={false} failed more={more} />);
    const made = observers.length;
    // The sentinel is still in view, so a live observer would ask at once. None may be made.
    expect(observers.length).toBe(made);
    observers.at(-1)?.(true);
    expect(more).not.toHaveBeenCalled();
    // The Load more button tries again: a load that succeeds re-arms the sentinel.
    rerender(<Probe enabled loading more={more} />);
    rerender(<Probe enabled loading={false} failed={false} more={more} />);
    observers.at(-1)?.(true);
    expect(more).toHaveBeenCalledTimes(1);
  });

  it("watches nothing when there is no next page", () => {
    render(<Probe enabled={false} loading={false} more={vi.fn()} />);
    expect(observers).toHaveLength(0);
  });

  it("does not ask when the sentinel leaves the view", () => {
    const more = vi.fn();
    render(<Probe enabled loading={false} more={more} />);
    observers.at(-1)?.(false);
    expect(more).not.toHaveBeenCalled();
  });
});
