// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { myBreakdown, myStorage } from "../lib/storage/api";
import { setLang, translate } from "../lib/i18n";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { desktopViewport, phoneViewport } from "../test/viewport";
import StoragePage from "./StoragePage";

function signedAs(id: string, role: "owner" | "member" | "admin" = "member"): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role, locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const largest = (id: string, name: string, size = 500, in_trash = false) => ({
  id,
  name,
  size,
  versions_bytes: 0,
  modified_at: "2026-10-01T00:00:00Z",
  in_trash,
});

// The page reads GET /me with the breakdown asked for (decision 79).
const ME = "/api/v1/me?storage=breakdown";
const LARGEST = "/api/v1/me/storage/largest";
const BREAKDOWN = { versions_bytes: 0, versions_files: 0, trash_bytes: 0, trash_files: 0 };
const STORAGE = { used_bytes: 4_000_000_000, reserved_bytes: 0, quota_bytes: 10_000_000_000, available_bytes: 6_000_000_000, breakdown: BREAKDOWN };

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <StoragePage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  session.set(signedAs("olivia"));
});

afterEach(() => {
  session.set(null);
  myStorage.set(null);
  myBreakdown.set(null);
});

describe("Storage", () => {
  it("shows the figures against the limit, with the meter, and the largest files with their address", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [largest("nod_big", "Movie.mkv", 3_000_000_000, true)] } };
      return fail(404, "x");
    });
    renderPage();
    const meter = await screen.findByRole("img", { name: /of 10 GB used/ });
    expect(meter.getAttribute("aria-label")).toContain("files 4 GB");
    const link = await screen.findByRole("link", { name: "Movie.mkv" });
    expect(link.getAttribute("href")).toBe("/n/nod_big");
    expect(screen.getAllByText(translate("en", "storage.inTrash")).length).toBeGreaterThan(0);
    expect(screen.queryByText("nod_big")).toBeNull();
  });

  it("with no limit the meter says so and has no free part", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: { ...STORAGE, quota_bytes: null, available_bytes: null } } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    expect(await screen.findByText(translate("en", "storage.meter.noLimit"))).toBeTruthy();
    expect(screen.getByRole("img", { name: /no limit/ })).toBeTruthy();
    expect(screen.queryByText(translate("en", "storage.free"))).toBeNull();
  });

  // The old versions and the trash come from the breakdown: a card for each that has something to free (decision 79).
  it("shows the Free up space cards only for what has something to free, and Go to Trash lands on the trash", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME)
        return {
          status: 200,
          body: { user: {}, storage: { ...STORAGE, breakdown: { versions_bytes: 4_100_000, versions_files: 6, trash_bytes: 0, trash_files: 0 } } },
        };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    expect(await screen.findByText(translate("en", "storage.free.versionsText", { size: "4.1 MB", files: "6 files" }))).toBeTruthy();
    expect(screen.queryByText(translate("en", "storage.free.trashTitle"))).toBeNull();
  });

  it("shows the trash card with Go to Trash, linking to the trash, when the trash has files", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME)
        return { status: 200, body: { user: {}, storage: { ...STORAGE, breakdown: { versions_bytes: 0, versions_files: 0, trash_bytes: 7_900_000, trash_files: 3 } } } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    expect(await screen.findByText(translate("en", "storage.free.trashTitle"))).toBeTruthy();
    expect(screen.getByRole("link", { name: translate("en", "storage.trashLink") }).getAttribute("href")).toBe("/trash");
    expect(screen.queryByText(translate("en", "storage.oldVersions"), { selector: "strong" })).toBeNull();
  });

  // A chip asks the server for its kind, and the list is read again from the first page.
  it("a filter chip asks for its kind and reads the first page again", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [largest("nod_a", "Song.mp3")] } };
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "Song.mp3" });
    fireEvent.click(screen.getByRole("button", { name: translate("en", "storage.chip.audio") }));
    await waitFor(() => expect(calls.some((c) => c.url.startsWith(LARGEST) && c.url.includes("kind=audio"))).toBe(true));
    expect(screen.getByRole("button", { name: translate("en", "storage.chip.audio") }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: translate("en", "storage.chip.trash") }).getAttribute("aria-pressed")).toBe("false");
  });

  it("a failed first load shows its error and never the empty state (guard: empty only after a successful read)", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      return fail(500, "internal");
    });
    renderPage();
    await screen.findByRole("alert");
    expect(screen.queryByText(translate("en", "storage.largestEmpty"))).toBeNull();
  });

  it("an empty answer shows the empty state", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    expect(await screen.findByText(translate("en", "storage.largestEmpty"))).toBeTruthy();
  });

  it("the next page is added to the list; a failed next page keeps the list and shows the error (guard: list kept on a failed load more)", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.includes("cursor=")) return fail(500, "internal");
      return { status: 200, body: { items: [largest("nod_1", "One.bin")], next_cursor: "c2" } };
    });
    renderPage();
    await screen.findByRole("link", { name: "One.bin" });
    fireEvent.click(screen.getByRole("button", { name: translate("en", "common.loadMore") }));
    await screen.findByRole("alert");
    expect(screen.getByRole("link", { name: "One.bin" })).toBeTruthy();
  });

  it("an installation admin reads nothing and sees no page (role refusal, guard: admin check before the reads)", async () => {
    session.set(signedAs("ada", "admin"));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [] } }));
    const { container } = renderPage();
    await new Promise((r) => setTimeout(r, 20));
    expect(calls.length).toBe(0);
    expect(container.textContent).toBe("");
  });

  it("a session change clears the last person's files before the next person's read is answered (guard: the list is reset on a new session)", async () => {
    let who = "ann";
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    stubFetch(async (c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) {
        if (who === "bob") await gate;
        return { status: 200, body: { items: [largest(`nod_${who}`, `${who}.bin`)] } };
      }
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "ann.bin" });
    who = "bob";
    session.set(signedAs("bob"));
    await waitFor(() => expect(screen.queryByRole("link", { name: "ann.bin" })).toBeNull());
    release();
    await screen.findByRole("link", { name: "bob.bin" });
    expect(within(document.body).queryByRole("link", { name: "ann.bin" })).toBeNull();
  });

  // A failed read of the figures says so, in place of the meter: a silent gap would look like "no limit".
  it("a failed read of the figures shows the error in place of the meter (guard: figures error shown)", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return fail(500, "internal");
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [] } };
      return fail(404, "x");
    });
    renderPage();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByRole("img", { name: /used/ })).toBeNull();
  });

  // A change of language keeps the list on screen and does not read it again: the page reads the language when it needs it.
  it("a change of language keeps the list and does not read it again (guard: lang read through a ref)", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [largest("nod_big", "Movie.mkv", 3_000_000_000, true)] } };
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "Movie.mkv" });
    const before = calls.filter((c) => c.url.startsWith(LARGEST)).length;
    await act(async () => {
      await setLang("he");
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByRole("link", { name: "Movie.mkv" })).toBeTruthy();
    expect(calls.filter((c) => c.url.startsWith(LARGEST)).length).toBe(before);
  });
});

// Review fixes (decision 79): the row actions, their effect on the server and on the list, the focus after one, the stale
// answer of a chip, and the size bar of each row.
describe("Storage row actions and the review fixes (decision 79)", () => {
  let restoreView: () => void = () => undefined;
  beforeEach(async () => {
    // The language test above leaves Hebrew on; these tests name the English labels.
    await setLang("en");
    // The row icons are drawn on a desktop window; a phone keeps the ⋮ menu alone (decision 122).
    restoreView = desktopViewport();
    session.set(signedAs("olivia"));
  });

  afterEach(() => {
    restoreView();
    session.set(null);
    myStorage.set(null);
    myBreakdown.set(null);
  });

  it("Move to trash is named for its file, sends the delete, then reads the figures and the list again", async () => {
    let trashed = false;
    const { calls } = stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.method === "DELETE") {
        trashed = true;
        return { status: 204 };
      }
      if (c.url.startsWith(LARGEST)) {
        return { status: 200, body: { items: trashed ? [largest("nod_2", "Two.bin", 100)] : [largest("nod_1", "One.bin"), largest("nod_2", "Two.bin", 100)] } };
      }
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "One.bin" });
    // Download is a link to the file's address, with its tooltip.
    expect(screen.getByRole("link", { name: "Download: One.bin" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Move to trash: One.bin" }));
    await waitFor(() => expect(screen.queryByRole("link", { name: "One.bin" })).toBeNull());
    const del = calls.findIndex((c) => c.method === "DELETE");
    expect(calls[del].url).toBe("/api/v1/nodes/nod_1");
    expect(calls.slice(del + 1).some((c) => c.url === ME)).toBe(true);
    expect(calls.slice(del + 1).some((c) => c.url.startsWith(LARGEST))).toBe(true);
  });

  it("Restore is named for its file, sends the restore for a file in the trash, then reads the list again", async () => {
    let restored = false;
    const { calls } = stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.method === "POST" && c.url.endsWith("/restore")) {
        restored = true;
        return { status: 200, body: { id: "nod_big", name: "Movie.mkv" } };
      }
      if (c.url.startsWith(LARGEST)) {
        return { status: 200, body: { items: restored ? [] : [largest("nod_big", "Movie.mkv", 3_000_000_000, true)] } };
      }
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "Movie.mkv" });
    fireEvent.click(screen.getByRole("button", { name: "Restore: Movie.mkv" }));
    await waitFor(() => expect(screen.queryByRole("link", { name: "Movie.mkv" })).toBeNull());
    const post = calls.findIndex((c) => c.method === "POST");
    expect(calls[post].url).toBe("/api/v1/nodes/nod_big/restore");
    expect(calls.slice(post + 1).some((c) => c.url === ME)).toBe(true);
  });

  it("after Move to trash the focus goes to the next row's first button, and to the heading when no row is left", async () => {
    let trashed = false;
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.method === "DELETE") {
        trashed = true;
        return { status: 204 };
      }
      if (c.url.startsWith(LARGEST)) {
        return { status: 200, body: { items: trashed ? [largest("nod_2", "Two.bin", 100)] : [largest("nod_1", "One.bin"), largest("nod_2", "Two.bin", 100)] } };
      }
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "One.bin" });
    fireEvent.click(screen.getByRole("button", { name: "Move to trash: One.bin" }));
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("link", { name: "Download: Two.bin" })));
  });

  // On a phone a row's icons are in its ⋮ menu, so the focus after Move to trash goes to the next row's ⋮ button.
  it("on a phone, after Move to trash the focus goes to the next row's ⋮ button", async () => {
    let trashed = false;
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.method === "DELETE") {
        trashed = true;
        return { status: 204 };
      }
      if (c.url.startsWith(LARGEST)) {
        return { status: 200, body: { items: trashed ? [largest("nod_2", "Two.bin", 100)] : [largest("nod_1", "One.bin"), largest("nod_2", "Two.bin", 100)] } };
      }
      return fail(404, "x");
    });
    const undo = phoneViewport();
    try {
      renderPage();
      const first = await screen.findByRole("row", { name: "One.bin" });
      fireEvent.click(first.querySelector('button[aria-haspopup="menu"]') as HTMLElement);
      fireEvent.click(await screen.findByRole("menuitem", { name: "Move to trash: One.bin" }));
      await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("row", { name: "Two.bin" }).querySelector('button[aria-haspopup="menu"]')));
    } finally {
      undo();
    }
  });

  it("when the last row goes, the focus goes to the heading of the list", async () => {
    let trashed = false;
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.method === "DELETE") {
        trashed = true;
        return { status: 204 };
      }
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: trashed ? [] : [largest("nod_1", "One.bin")] } };
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "One.bin" });
    fireEvent.click(screen.getByRole("button", { name: "Move to trash: One.bin" }));
    await waitFor(() => expect(document.activeElement).toBe(document.getElementById("largest-title")));
  });

  // The table's paddings are logical, so a right-to-left page keeps the name's inset on its start side.
  // Decision 122 (tl3): the rows are the one table's rows, each named by its file, with the name in the first cell.
  it("each largest file is a table row named by the file, and its name is the first cell's link", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [largest("nod_1", "One.bin")] } };
      return fail(404, "x");
    });
    renderPage();
    const row = (await screen.findByRole("row", { name: "One.bin" })) as HTMLElement;
    expect(row.querySelector("td a")?.textContent).toContain("One.bin");
  });

  it("each row shows a size bar against the largest row, hidden from assistive technology", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [largest("nod_a", "A.bin", 500), largest("nod_b", "B.bin", 250)] } };
      return fail(404, "x");
    });
    const { container } = renderPage();
    await screen.findByRole("link", { name: "B.bin" });
    const bars = Array.from(container.querySelectorAll<HTMLElement>("[data-size-bar]"));
    expect(bars.map((b) => b.style.width)).toEqual(["100%", "50%"]);
    expect(bars.every((b) => b.closest('[aria-hidden="true"]') !== null)).toBe(true);
  });

  // A chip's answer that comes back after the next chip was picked must not end the loading of the next one.
  it("a stale answer for the previous chip does not show the empty state while the new chip is still loading", async () => {
    let releaseAudio!: () => void;
    let releaseVideo!: () => void;
    const audioGate = new Promise<void>((r) => (releaseAudio = r));
    const videoGate = new Promise<void>((r) => (releaseVideo = r));
    stubFetch(async (c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.includes("kind=audio")) {
        await audioGate;
        return { status: 200, body: { items: [largest("nod_a", "Song.mp3")] } };
      }
      if (c.url.includes("kind=video")) {
        await videoGate;
        return { status: 200, body: { items: [largest("nod_v", "Clip.mp4")] } };
      }
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    await screen.findByRole("status");
    fireEvent.click(screen.getByRole("button", { name: translate("en", "storage.chip.audio") }));
    fireEvent.click(screen.getByRole("button", { name: translate("en", "storage.chip.video") }));
    await act(async () => {
      releaseAudio();
      await new Promise((r) => setTimeout(r, 30));
    });
    expect(screen.queryByRole("status")).toBeNull();
    expect(document.querySelector(".ant-skeleton")).not.toBeNull();
    await act(async () => {
      releaseVideo();
    });
    await screen.findByRole("link", { name: "Clip.mp4" });
    expect(screen.queryByRole("link", { name: "Song.mp3" })).toBeNull();
  });
});
