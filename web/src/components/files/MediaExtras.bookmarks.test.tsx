// SPDX-License-Identifier: AGPL-3.0-or-later

// The More menu's bookmark items: Add a bookmark and Bookmarks (n), drawn only when the player has marks, with Add
// disabled until the list is read, when the file is full, or with no element.

import { fireEvent, render, screen } from "@testing-library/react";
import { App } from "antd";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { loadMedia } from "../../lib/media/i18n";
import type { Bookmark } from "../../lib/files/api";
import type { Marks } from "../../lib/files/bookmarks";
import MediaExtras from "./MediaExtras";

const b = (over: Partial<Bookmark> = {}): Bookmark => ({ id: "bmk_1", position_seconds: 10, label: "", created_at: "2026-11-16T00:00:00Z", ...over });

function marks(items: Bookmark[], over: Partial<Marks> = {}): Marks {
  return { items, ready: true, full: false, add: vi.fn(), openList: vi.fn(), ...over };
}

const host = () => document.body;
const el = () => document.createElement("audio");

function draw(m: HTMLMediaElement | null, mk?: Marks) {
  return render(
    <App>
      <MediaExtras media={m} popupHost={host} marks={mk} />
    </App>,
  );
}

async function openMenu() {
  fireEvent.click(screen.getByRole("button", { name: "More options" }));
  return screen.findByText("A-B loop");
}

beforeAll(async () => {
  await loadMedia("en");
});

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the More menu's bookmark items", () => {
  it("shows Add a bookmark and Bookmarks (n) only with marks, and they run the marks' actions", async () => {
    const mk = marks([b(), b({ id: "bmk_2" })]);
    draw(el(), mk);
    await openMenu();
    const add = await screen.findByRole("menuitem", { name: "Add a bookmark" });
    const list = await screen.findByRole("menuitem", { name: "Bookmarks (2)" });
    fireEvent.click(add);
    expect(mk.add).toHaveBeenCalledTimes(1);
    fireEvent.click(list);
    expect(mk.openList).toHaveBeenCalledTimes(1);
  });

  it("draws neither item without marks", async () => {
    draw(el());
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Add a bookmark" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: /^Bookmarks/ })).toBeNull();
  });

  it("disables Add until the list is read, when full, or with no element", async () => {
    const notReady = draw(el(), marks([], { ready: false }));
    await openMenu();
    expect((await screen.findByRole("menuitem", { name: "Add a bookmark" })).getAttribute("aria-disabled")).toBe("true");
    notReady.unmount();

    const full = draw(el(), marks([b()], { full: true }));
    await openMenu();
    expect((await screen.findByRole("menuitem", { name: "Add a bookmark" })).getAttribute("aria-disabled")).toBe("true");
    full.unmount();

    const noEl = draw(null, marks([b()]));
    await openMenu();
    expect((await screen.findByRole("menuitem", { name: "Add a bookmark" })).getAttribute("aria-disabled")).toBe("true");
  });
});
