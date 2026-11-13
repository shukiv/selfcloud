// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { desktopViewport, phoneViewport } from "../../test/viewport";
import LinksPage from "./LinksPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: true,
  } as unknown as Session;
}

const LINK = {
  id: "shl_1",
  node_id: "nod_1",
  audience: "anyone",
  role: "viewer",
  upload_only: false,
  expires_at: null,
  created_at: "2026-10-01T00:00:00Z",
  paused: false,
  password_set: false,
  created_by: "usr_owen",
  created_by_kind: "user",
  created_by_name: "Owen",
  folder_id: "fld_1",
  folder_kind: "team",
  folder_name: "Plans",
  folder_owner_id: "",
  folder_owner_name: "",
  item_name: "report.pdf",
  item_path: "Plans/report.pdf",
  hidden: false,
};

function renderPage() {
  const qc = createQueryClient();
  render(
    <App>
      <QueryClientProvider client={qc}>
        <LinksPage />
      </QueryClientProvider>
    </App>,
  );
  return qc;
}

// The row icons are drawn on a desktop window; a phone keeps the ⋮ menu alone (decision 122).
let restoreView: () => void = () => undefined;
beforeEach(() => {
  restoreView = desktopViewport();
  session.set(signedAs());
});
afterEach(() => {
  restoreView();
  session.set(null);
});

const isLinkDelete = (c: Call) => c.method === "DELETE" && c.url === "/api/v1/share-links/shl_1";

// Round 2 (LOW): a row opens its item, as Enter and a click open a row of any list (decision 122).
describe("a link row opens its item", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });
  const row = async () => (await screen.findByRole("region", { name: "Links" })).querySelector("tbody tr[data-id]") as HTMLElement;

  it("a click on the row opens the item it shares, in this tab", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    fireEvent.click(await row());
    expect(open).toHaveBeenCalledWith("/n/nod_1", "_self");
  });

  it("Enter on the row opens the item the same way", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    fireEvent.keyDown(await row(), { key: "Enter" });
    expect(open).toHaveBeenCalledWith("/n/nod_1", "_self");
  });

  // A hidden item opens nothing, whether or not its name came back: each of the two is a reason on its own.
  it.each([
    ["a hidden item, with its name", { hidden: true }],
    ["an item with no name the viewer may see", { item_name: null, item_path: null }],
  ])("%s does not open", async (_label, over) => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [{ ...LINK, ...over }] } };
    });
    renderPage();
    fireEvent.click(await row());
    expect(open).not.toHaveBeenCalled();
  });

  it("the row's Delete icon asks about the link and does not open the item", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Delete link: report.pdf" }));
    expect(open).not.toHaveBeenCalled();
  });
});

describe("links icon buttons name their item", () => {
  it("each row's Delete link names the item the link shares", async () => {
    const other = { ...LINK, id: "shl_2", item_name: "budget.xlsx", item_path: "Plans/budget.xlsx" };
    stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 2 } };
      return { status: 200, body: { items: [LINK, other] } };
    });
    renderPage();
    expect(await screen.findByRole("button", { name: "Delete link: report.pdf" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete link: budget.xlsx" })).toBeTruthy();
  });
});

describe("links", () => {
  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c) => (c.url.includes("/share-links") ? fail(403, "forbidden") : { status: 200, body: { anyone_links: 0, public_links: true } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("No links to show")).toBeNull();
  });

  // Decision 122 (tl3): the links are the one table. Its headers are the existing words, its rows are named by the item, and
  // an empty list keeps its header row with the title in the body.
  it("is one table whose headers keep the words and whose empty body keeps the headers (decision 122)", async () => {
    stubFetch((c: Call) => (c.url.includes("/share-links") || c.url.includes("/links") ? { status: 200, body: { items: [LINK] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Links" });
    // The data arrives after the first render, which is the phone layout until antd knows the breakpoint: the headers are awaited.
    await waitFor(() => expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Who can open", "Kind", "Created", "Status", "Actions"]));
    const row = region.querySelector('tbody tr[data-id="shl_1"]') as HTMLElement;
    expect(row.getAttribute("aria-label")).toBe("report.pdf");
    expect(row.querySelector(".item-name")?.textContent).toBe("report.pdf");
  });

  // Round 2 (H1, design section 4): at 375 px only the name column and the ⋮ menu show; the audience, kind, created and status
  // join the name cell's second line.
  it("at 375 px only the name and the ⋮ column show, and the other columns join the name cell (design section 4)", async () => {
    stubFetch((c: Call) => (c.url.includes("/share-links") || c.url.includes("/links") ? { status: 200, body: { items: [LINK] } } : { status: 200, body: { items: [] } }));
    const undo = phoneViewport();
    try {
      renderPage();
      const region = await screen.findByRole("region", { name: "Links" });
      expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Actions"]);
      const name = region.querySelector('tbody tr[data-id="shl_1"] td') as HTMLElement;
      expect(name.textContent).toContain("Anyone with the link");
      expect(name.textContent).toContain("Access link");
    } finally {
      undo();
    }
  });

  it("an empty list keeps its header row, and its title is in the table body (decision 122)", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Links" });
    // An empty list has no row icons, so no Actions column (it comes with the first row's icons).
    await waitFor(() => expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Who can open", "Kind", "Created", "Status"]));
    expect(region.querySelector("tbody .empty-title")?.textContent).toBe("No links to show");
    // Round 2 (M3): the empty table says what will show here, in the admin catalogue (loaded on demand).
    await waitFor(() => expect(region.querySelector("tbody .empty-desc")?.textContent).toBe("Drop links and links that anyone can open show here."));
  });

  it("a failed read of the count shows an error, not a zero (guard: no silent zero for the delete-all count)", async () => {
    stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return fail(403, "forbidden");
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    expect(await screen.findByText("report.pdf")).toBeTruthy();
    expect((await screen.findByRole("alert")).className).toContain("ant-alert-error");
    expect(screen.queryByRole("button", { name: "Delete all public links" })).toBeNull();
  });

  // Round 2 (M5): Load more appends the next page of links; a failed next page keeps the first one, with the error.
  const SECOND_LINK = { ...LINK, id: "shl_2", item_name: "budget.xlsx", item_path: "Plans/budget.xlsx" };
  it("Load more appends the next page of links", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/share-links")) {
        return c.url.includes("c2") ? { status: 200, body: { items: [SECOND_LINK] } } : { status: 200, body: { items: [LINK], next_cursor: "c2" } };
      }
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Links" });
    expect(await within(region).findByText("report.pdf")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await within(region).findByText("budget.xlsx")).toBeTruthy();
    expect(within(region).getByText("report.pdf")).toBeTruthy();
  });

  it("a failed next page keeps the first one and shows the error", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/share-links")) {
        return c.url.includes("c2") ? fail(403, "forbidden") : { status: 200, body: { items: [LINK], next_cursor: "c2" } };
      }
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Links" });
    expect(await within(region).findByText("report.pdf")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(within(region).getByText("report.pdf")).toBeTruthy();
  });

  it("the audience filter asks the server for that audience (guard: filter in the request)", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    await screen.findByText("report.pdf");
    const selector = document.getElementById("links-audience")!.closest(".ant-select")!.querySelector(".ant-select-selector") as HTMLElement;
    fireEvent.mouseDown(selector);
    const options = await screen.findAllByText("Anyone with the link");
    fireEvent.click(options[options.length - 1]);
    await waitFor(() => expect(calls.some((c) => c.url.includes("audience=anyone"))).toBe(true));
  });

  it("a delete that never answers: Cancel closes the dialog and aborts the request (fu4, item 2)", async () => {
    const { calls } = stubFetch((c) => {
      if (isLinkDelete(c)) return new Promise<Answer>(() => undefined);
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /^Delete link/ }));
    const go = (await screen.findAllByRole("button", { name: /Delete link/ })).at(-1)!;
    fireEvent.click(go);
    await waitFor(() => expect(calls.filter(isLinkDelete)).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find(isLinkDelete)?.signal?.aborted).toBe(true);
  });

  it("deleting a link asks first and sends one request for a double click (guard: confirm and once-at-a-time)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    const { calls } = stubFetch((c) => {
      if (isLinkDelete(c)) return held;
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /^Delete link/ }));
    const go = (await screen.findAllByRole("button", { name: /Delete link/ })).at(-1)!;
    act(() => {
      fireEvent.click(go);
      fireEvent.click(go);
    });
    await waitFor(() => expect(calls.filter(isLinkDelete)).toHaveLength(1));
    release({ status: 204 });
    expect(await screen.findByText("Link deleted.")).toBeTruthy();
  });
});
