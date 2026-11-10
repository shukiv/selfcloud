// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../test/fetchStub";
import SearchPage, { mergeHits, searchErrorText } from "./SearchPage";

const SEARCH = "/api/v1/search";
const BOX = "Search names and text in files";
const FIELDS = {
  items: [
    { id: "fld_cl1", name: "Client", type: "text", restricted: false, choices: [] },
    { id: "fld_st1", name: "Stage", type: "choice", restricted: false, choices: [{ id: "chc_open", name: "Open", archived: false }] },
  ],
};

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const hit = (id: string, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  folder_id: "fld_a",
  parent_id: null,
  name,
  kind: "file",
  size: 2048,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-02T00:00:00Z",
  role: "viewer",
  owner_id: "usr_a",
  no_download: false,
  editors_can_share: false,
  ...extra,
});

function renderAt(url: string) {
  return renderEntry(url);
}

// renderEntry opens the page at an address, or at an entry with its own state (as a link from the top bar gives it).
function renderEntry(entry: string | { pathname: string; search: string; state: unknown }) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={[entry as never]}>
        <Routes>
          <Route path="/search" element={<SearchPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// searches returns the calls the page made to the search route.
const searches = (calls: Call[]) => calls.filter((c) => c.url.startsWith(SEARCH));

// answerEach stubs the search route with one answer for every call, and the label fields.
function answerEach(answer: (c: Call) => Answer | Promise<Answer>) {
  return stubFetch((c) => {
    if (c.url.startsWith("/api/v1/label-fields")) return { status: 200, body: FIELDS };
    if (!c.url.startsWith(SEARCH)) return fail(404, "not_found");
    return answer(c);
  });
}

// deferred stubs the search route, holding each answer until the test resolves it.
function deferred() {
  const held: { url: string; resolve: (a: Answer) => void }[] = [];
  const { calls } = stubFetch((c) => {
    if (c.url.startsWith("/api/v1/label-fields")) return { status: 200, body: FIELDS };
    if (!c.url.startsWith(SEARCH)) return fail(404, "not_found");
    return new Promise<Answer>((resolve) => held.push({ url: c.url, resolve }));
  });
  return { calls, held };
}

const ok = (items: unknown[], next?: string): Answer => ({ status: 200, body: next ? { items, next_cursor: next } : { items } });

beforeEach(() => {
  session.set(signedAs("olivia"));
});

afterEach(() => {
  session.set(null);
});

describe("the search page", () => {
  it("asks for words before it sends anything", async () => {
    const { calls } = answerEach(() => ok([]));
    renderAt("/search");
    expect(await screen.findByText("Type a word, or choose a filter.")).toBeTruthy();
    expect(searches(calls.map((c) => c))).toHaveLength(0);
  });

  it("sends one request for a burst of typing, with the words and the page size, and shows names and never IDs", async () => {
    const { calls } = answerEach(() => ok([hit("nod_a1b2", "Budget 2026.txt")]));
    renderAt("/search");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(BOX), "budget");
    await waitFor(() => expect(searches(calls)).toHaveLength(1));
    await new Promise((r) => setTimeout(r, 400));
    expect(searches(calls)).toHaveLength(1);
    const url = new URL(searches(calls)[0].url, "http://x");
    expect(url.searchParams.get("q")).toBe("budget");
    expect(url.searchParams.get("limit")).toBe("25");
    expect(await screen.findByText("Budget 2026.txt")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/nod_a1b2/);
  });

  it("a one-character word is not sent, and the sentence says why", async () => {
    const { calls } = answerEach(() => ok([]));
    renderAt("/search");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(BOX), "a");
    expect(await screen.findByText("Each word needs at least two letters or digits.")).toBeTruthy();
    await new Promise((r) => setTimeout(r, 400));
    expect(searches(calls)).toHaveLength(0);
  });

  it("an empty answer shows the empty state, and no count", async () => {
    answerEach(() => ok([]));
    renderAt("/search?q=zzzz");
    expect(await screen.findByText(/Nothing matches these words and filters/)).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/\b0 results?\b/);
  });

  it("a first answer that fails shows its error, and no empty state", async () => {
    answerEach(() => fail(500, "unavailable"));
    renderAt("/search?q=budget");
    expect(await screen.findByText("The search took too long. Try narrower words or filters.")).toBeTruthy();
    expect(screen.queryByText(/Nothing matches these words/)).toBeNull();
  });

  it("a rate limit reads as a sentence, not as a code", async () => {
    answerEach(() => fail(429, "rate_limited"));
    renderAt("/search?q=budget");
    expect(await screen.findByText("Too many searches. Wait a minute, then try again.")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/rate_limited/);
  });

  it("a failed refetch keeps the list it had, and says why above it", async () => {
    let n = 0;
    answerEach(() => {
      n += 1;
      return n === 1 ? ok([hit("nod_a1", "Alpha.txt")], "cur_1") : fail(429, "rate_limited");
    });
    renderAt("/search?q=alpha");
    expect(await screen.findByText("Alpha.txt")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Too many searches. Wait a minute, then try again.")).toBeTruthy();
    expect(screen.getByText("Alpha.txt")).toBeTruthy();
  });

  it("only the answer to the latest address is shown, even when it comes back first", async () => {
    const { calls, held } = deferred();
    renderAt("/search?q=alpha");
    const user = userEvent.setup();
    await waitFor(() => expect(held).toHaveLength(1));
    const box = screen.getByLabelText(BOX);
    await user.clear(box);
    await user.type(box, "beta");
    await waitFor(() => expect(held).toHaveLength(2));
    await act(async () => held[1].resolve(ok([hit("nod_b", "Beta.txt")])));
    expect(await screen.findByText("Beta.txt")).toBeTruthy();
    await act(async () => held[0].resolve(ok([hit("nod_a", "Alpha.txt")])));
    expect(screen.queryByText("Alpha.txt")).toBeNull();
    expect(searches(calls)).toHaveLength(2);
  });

  it("a late failure of an old address is not shown", async () => {
    const { held } = deferred();
    renderAt("/search?q=alpha");
    const user = userEvent.setup();
    await waitFor(() => expect(held).toHaveLength(1));
    const box = screen.getByLabelText(BOX);
    await user.clear(box);
    await user.type(box, "beta");
    await waitFor(() => expect(held).toHaveLength(2));
    await act(async () => held[1].resolve(ok([hit("nod_b", "Beta.txt")])));
    await act(async () => held[0].resolve(fail(500, "internal")));
    expect(await screen.findByText("Beta.txt")).toBeTruthy();
    expect(screen.queryByText("The server had a problem. Try again in a moment.")).toBeNull();
  });

  it("more may follow is said, and Load more adds the next page without repeating a node", async () => {
    let n = 0;
    answerEach(() => {
      n += 1;
      return n === 1 ? ok([hit("nod_a", "A.txt"), hit("nod_b", "B.txt")], "cur_1") : ok([hit("nod_b", "B.txt"), hit("nod_c", "C.txt")]);
    });
    renderAt("/search?q=file");
    expect(await screen.findByText("More results may follow.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("C.txt")).toBeTruthy();
    expect(screen.getAllByText("B.txt")).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("Load more asks with the same window as the first page: the cursor is bound to it", async () => {
    // The server's cursor fingerprint includes the modified-after time, so a later page must not take a new clock.
    let now = 1_800_000_000_000;
    const spy = vi.spyOn(Date, "now").mockImplementation(() => (now += 1000));
    try {
      let n = 0;
      const { calls } = answerEach(() => {
        n += 1;
        return n === 1 ? ok([hit("nod_a", "A.txt")], "cur_1") : ok([hit("nod_b", "B.txt")]);
      });
      renderAt("/search?q=file&modified=7");
      expect(await screen.findByText("A.txt")).toBeTruthy();
      fireEvent.click(screen.getByRole("button", { name: "Load more" }));
      expect(await screen.findByText("B.txt")).toBeTruthy();
      const [first, second] = searches(calls).map((c) => new URL(c.url, "http://x").searchParams);
      expect(second.get("cursor")).toBe("cur_1");
      expect(second.get("modified_after")).toBe(first.get("modified_after"));
    } finally {
      spy.mockRestore();
    }
  });

  it("a page with no hits but a cursor says more may follow, not that nothing matches", async () => {
    answerEach(() => ok([], "cur_9"));
    renderAt("/search?q=file");
    expect(await screen.findByText(/Nothing on this part of the results matched/)).toBeTruthy();
    expect(screen.queryByText(/Nothing matches these words/)).toBeNull();
  });

  it("a text search marks the matched words and shows the rest of the snippet as text, never as markup", async () => {
    const text = "<img src=x onerror=alert(1)> דוח שנתי";
    const start = new TextEncoder().encode("<img src=x onerror=alert(1)> דוח ").length;
    const end = start + new TextEncoder().encode("שנתי").length;
    answerEach(() =>
      ok([hit("nod_t", "Report.txt", { snippet: { text, ranges: [[start, end]] } })]),
    );
    renderAt("/search?q=שנתי&in=content");
    const mark = await waitFor(() => {
      const m = document.querySelector("mark");
      if (!m) throw new Error("no mark yet");
      return m;
    });
    expect(mark.textContent).toBe("שנתי");
    expect(document.querySelector("img")).toBeNull();
    expect(document.body.textContent ?? "").toContain("<img src=x onerror=alert(1)>");
  });

  it("a text search with no words says it needs one, and sends nothing", async () => {
    const { calls } = answerEach(() => ok([]));
    renderAt("/search?in=content");
    expect(await screen.findByText("A text search needs a word to look for.")).toBeTruthy();
    expect(searches(calls)).toHaveLength(0);
  });

  it("a chip removes its filter alone, and the search is sent again without it", async () => {
    const { calls } = answerEach(() => ok([hit("nod_a", "Alpha.txt")]));
    renderAt("/search?q=alpha&kind=file");
    await screen.findByText("Alpha.txt");
    expect(searches(calls)[0].url).toContain("kind=file");
    fireEvent.click(screen.getByRole("button", { name: "Remove filter Type: Files" }));
    await waitFor(() => expect(searches(calls)).toHaveLength(2));
    const last = searches(calls)[1].url;
    expect(last).toContain("q=alpha");
    expect(last).not.toContain("kind=");
  });

  it("opened from the phone's button, the page takes the focus for its own box", async () => {
    answerEach(() => ok([]));
    renderEntry({ pathname: "/search", search: "", state: { focusSearch: true } });
    const box = await screen.findByLabelText(BOX);
    await waitFor(() => expect(document.activeElement).toBe(box));
  });

  it("while the label fields are read, a label chip is neutral: no value, no ID", async () => {
    stubFetch((c) => {
      if (c.url.startsWith("/api/v1/label-fields")) return new Promise<Answer>(() => undefined);
      return fail(404, "not_found");
    });
    renderAt("/search?q=alpha&label=fld_cl1:Acme");
    expect(await screen.findByText("Label")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/Acme|fld_cl1/);
  });

  it("a label filter of a field the account does not have is not sent, and its ID is not shown", async () => {
    const { calls } = answerEach(() => ok([hit("nod_a", "Alpha.txt")]));
    renderAt("/search?q=alpha&label=fld_zz9:x");
    await screen.findByText("Alpha.txt");
    await new Promise((r) => setTimeout(r, 50));
    expect(searches(calls).some((c) => c.url.includes("fld_zz9"))).toBe(false);
    expect(document.body.textContent ?? "").not.toMatch(/fld_zz9/);
  });

  it("a crafted choice ID that is not a choice of its field is not sent, and is not shown", async () => {
    const { calls } = answerEach(() => ok([hit("nod_a", "Alpha.txt")]));
    renderAt("/search?q=alpha&label=fld_st1:cho_xyz");
    await screen.findByText("Alpha.txt");
    expect(searches(calls).some((c) => c.url.includes("cho_xyz"))).toBe(false);
    expect(document.body.textContent ?? "").not.toMatch(/cho_xyz/);
  });

  it("when the label fields cannot be read, the label filters are not sent and the search runs without them", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith("/api/v1/label-fields")) return fail(500, "internal");
      if (!c.url.startsWith(SEARCH)) return fail(404, "not_found");
      return ok([hit("nod_a", "Alpha.txt")]);
    });
    renderAt("/search?q=alpha&label=fld_cl1:Acme");
    await screen.findByText("Alpha.txt");
    expect(searches(calls).some((c) => c.url.includes("label="))).toBe(false);
    expect(document.body.textContent ?? "").not.toMatch(/Client: Acme|fld_cl1/);
  });

  it("a label chip names the field and never shows its ID", async () => {
    answerEach(() => ok([hit("nod_a", "Alpha.txt")]));
    renderAt("/search?q=alpha&label=fld_cl1:Acme");
    expect(await screen.findByText("Client: Acme")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/fld_cl1/);
  });
});

describe("the page's helpers", () => {
  it("mergeHits keeps the first copy of a node", () => {
    const a = hit("nod_a", "A") as never;
    const b = hit("nod_b", "B") as never;
    expect(mergeHits([a, b], [b, hit("nod_c", "C") as never]).map((h) => h.id)).toEqual(["nod_a", "nod_b", "nod_c"]);
  });

  it("searchErrorText reads a code, and falls back to the file errors for the rest", () => {
    expect(searchErrorText(new (class extends Error {})(), "en")).toBe("Something went wrong. Try again.");
  });
});
