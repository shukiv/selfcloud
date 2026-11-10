// SPDX-License-Identifier: AGPL-3.0-or-later

// The sentinel asks for the next page once each time its element comes into view, and never while a page loads (tl4 r1,
// finding 2; design 2.2, paging).
import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLoadMoreSentinel } from "./loadMoreSentinel";

type Fire = (isIntersecting: boolean) => void;
let observers: Fire[] = [];

class FakeObserver {
  constructor(private readonly cb: (entries: { isIntersecting: boolean }[]) => void) {
    observers.push((isIntersecting) => this.cb([{ isIntersecting }]));
  }
  observe() {}
  disconnect() {}
  unobserve() {}
  takeRecords() {
    return [];
  }
}

function Probe(props: { enabled: boolean; loading: boolean; more: () => void }) {
  const ref = useLoadMoreSentinel(props.enabled, props.loading, props.more);
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

  it("watches again once the load ends, so a sentinel still in view asks for the next page", () => {
    const more = vi.fn();
    const { rerender } = render(<Probe enabled loading more={more} />);
    rerender(<Probe enabled loading={false} more={more} />);
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
