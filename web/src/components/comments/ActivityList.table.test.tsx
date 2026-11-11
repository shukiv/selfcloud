// SPDX-License-Identifier: AGPL-3.0-or-later

// The activity of an item on the one table (tl5, decision 138), in the comments panel: When first and marked newest first, the event
// as the line under it at any width (the panel is narrow, so the phone layout is always used), the frame and the words of an empty
// list, and an entry's time as a time element.
import { act, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { loadComments } from "../../lib/comments/i18n";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { desktopViewport } from "../../test/viewport";
import ActivityList from "./ActivityList";

beforeAll(async () => {
  await loadComments("en");
});

const entry = {
  id: "h1",
  time: "2026-11-01T10:00:00Z",
  kind: "comment",
  action: "comment.create",
  actor: { kind: "user", name: "Ann" },
  details: { reply: false },
};

function renderList(items: unknown[]) {
  stubFetch((c: Call): Answer => {
    if (c.url.startsWith("/api/v1/nodes/nod_1/activity")) return { status: 200, body: { items } };
    return fail(404, "not_found");
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ActivityList nodeID="nod_1" />
    </QueryClientProvider>,
  );
}

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
});
afterEach(() => {
  undoView();
});

describe("the activity is one table in its panel (tl5, decision 138)", () => {
  it("draws When first, marked newest first, with the event as the line under it, on a desktop too", async () => {
    renderList([entry]);
    const region = await screen.findByRole("region", { name: "Activity" });
    const when = within(region).getByRole("columnheader", { name: "When" });
    expect(when.getAttribute("aria-sort")).toBe("descending");
    const row = await screen.findByTestId("activity-entry");
    expect(row.getAttribute("role")).toBe("row");
    // The name is set between bidi isolation marks, which the comments tests strip the same way.
    expect((row.querySelector(".row-second")?.textContent ?? "").replace(/[⁦-⁩]/g, "")).toContain("Ann commented on an item.");
    expect(within(region).queryByRole("columnheader", { name: "Event" })).toBeNull();
  });

  it("keeps the table's frame when nothing is recorded, and says so in its body", async () => {
    renderList([]);
    const region = await screen.findByRole("region", { name: "Activity" });
    expect(within(region).getByRole("columnheader", { name: "When" })).toBeTruthy();
    expect(within(region).getByText("Nothing recorded yet")).toBeTruthy();
    expect(within(region).getByText("Nothing has been recorded for this item yet.")).toBeTruthy();
  });

  it("asks for the next page through the sentinel, and a failed next page is asked for once, not again by itself", async () => {
    const before = (window as unknown as { IntersectionObserver?: unknown }).IntersectionObserver;
    // Each observer reports its element as in view at once, which is what the sentinel does for a short page.
    let watched = 0;
    class InView {
      constructor(private cb: (entries: { isIntersecting: boolean }[]) => void) {}
      observe() {
        watched++;
        this.cb([{ isIntersecting: true }]);
      }
      disconnect() {}
      unobserve() {}
    }
    (window as unknown as { IntersectionObserver: unknown }).IntersectionObserver = InView;
    try {
      const { calls } = stubFetch(async (c: Call): Promise<Answer> => {
        if (!c.url.startsWith("/api/v1/nodes/nod_1/activity")) return fail(404, "not_found");
        if (!c.url.includes("cursor=")) return { status: 200, body: { items: [entry], next_cursor: "c1" } };
        // The page takes time to fail, as a network does, so the list is seen loading before it is seen failed.
        await new Promise((r) => setTimeout(r, 20));
        return fail(500, "internal");
      });
      const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      render(
        <QueryClientProvider client={client}>
          <ActivityList nodeID="nod_1" />
        </QueryClientProvider>,
      );
      await screen.findByText(/could not be refreshed/);
      // The effects of the commit that showed the failure have run: the end of the list is not watched again.
      await act(async () => {});
      expect(watched).toBe(1);
      expect(calls.filter((c) => c.url.includes("cursor=c1"))).toHaveLength(1);
    } finally {
      (window as unknown as { IntersectionObserver?: unknown }).IntersectionObserver = before;
    }
  });

  it("gives the time as a time element with its ISO value", async () => {
    renderList([entry]);
    const row = await screen.findByTestId("activity-entry");
    expect(row.querySelector("time")?.getAttribute("datetime")).toBe("2026-11-01T10:00:00Z");
  });
});
