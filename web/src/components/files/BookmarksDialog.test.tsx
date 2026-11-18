// SPDX-License-Identifier: AGPL-3.0-or-later

// The bookmarks dialog: the file's places as a table, the row's Go to / Rename / Delete, the empty state with its add
// action, and a failed read's Retry. It is the lazy dialog the hook opens; here it is drawn directly with its props.

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { App } from "antd";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/api";
import { loadMedia } from "../../lib/media/i18n";
import type { Bookmark } from "../../lib/files/api";
import { LABEL_MAX, type Marks } from "../../lib/files/bookmarks";
import BookmarksDialog from "./BookmarksDialog";

const b = (over: Partial<Bookmark> = {}): Bookmark => ({
  id: "bmk_1",
  position_seconds: 42,
  label: "",
  created_at: "2026-11-16T00:00:00Z",
  ...over,
});

function media(): HTMLMediaElement {
  const m = document.createElement("audio");
  Object.defineProperty(m, "currentTime", { value: 0, writable: true, configurable: true });
  return m;
}

function marks(items: Bookmark[], over: Partial<Marks> = {}): Marks {
  return { items, ready: true, full: false, add: vi.fn(), openList: vi.fn(), ...over };
}

function draw(
  items: Bookmark[],
  over: {
    marks?: Partial<Marks>;
    readFailed?: boolean;
    media?: HTMLMediaElement | null;
    onRelabel?: (id: string, label: string) => Promise<Bookmark>;
    onRemove?: (id: string) => Promise<void>;
  } = {},
) {
  const props = {
    name: "song.mp3",
    marks: marks(items, over.marks),
    media: over.media === undefined ? media() : over.media,
    readFailed: over.readFailed ?? false,
    onRelabel: over.onRelabel ?? vi.fn(async (_id: string, label: string) => b({ label })),
    onRemove: over.onRemove ?? vi.fn(async () => {}),
    onRetry: vi.fn(),
    onClose: vi.fn(),
  };
  const utils = render(
    <App>
      <BookmarksDialog {...props} />
    </App>,
  );
  return { ...utils, props };
}

const rows = () => screen.getAllByTestId("bookmark-row");
const notices = () => [...document.querySelectorAll(".ant-message-notice")].map((n) => n.textContent ?? "");

beforeAll(async () => {
  await loadMedia("en");
});

beforeEach(() => {
  // A desktop window with a mouse, so the row's three icons show inline (the shared preset list's rule).
  vi.stubGlobal("matchMedia", (query: string) => {
    const m = /min-width: (\d+)px/.exec(query);
    const matches = m ? 1280 >= Number(m[1]) : false;
    return { matches, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
  });
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("the table", () => {
  it("shows the file's name, each row's time, and its label or the no-label words", () => {
    draw([b({ id: "bmk_1", position_seconds: 42, label: "Intro" }), b({ id: "bmk_2", position_seconds: 90, label: "" })]);
    expect(screen.getByText("Bookmarks")).toBeTruthy();
    expect(screen.getByText("song.mp3")).toBeTruthy();
    expect(rows().length).toBe(2);
    expect(within(rows()[0]).getByText("0:42")).toBeTruthy();
    expect(within(rows()[0]).getByText("Intro")).toBeTruthy();
    expect(within(rows()[1]).getByText("1:30")).toBeTruthy();
    expect(within(rows()[1]).getByText("No label")).toBeTruthy();
  });

  it("Go to seeks the element to the place, keeps play/pause, and closes the dialog", () => {
    const m = media();
    const play = vi.spyOn(m, "play");
    const pause = vi.spyOn(m, "pause");
    const { props } = draw([b({ position_seconds: 42 })], { media: m });
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Go to" }));
    expect(m.currentTime).toBe(42);
    expect(play).not.toHaveBeenCalled();
    expect(pause).not.toHaveBeenCalled();
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });

  it("Delete removes the row through remove", () => {
    const { props } = draw([b({ id: "bmk_1" })]);
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Delete" }));
    expect(props.onRemove).toHaveBeenCalledWith("bmk_1");
  });

  it("two Delete presses on one row send one remove and show no error", async () => {
    let resolveDelete: () => void = () => {};
    const onRemove = vi.fn(() => new Promise<void>((res) => { resolveDelete = res; }));
    draw([b({ id: "bmk_1" })], { onRemove });
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Delete" }));
    // While the delete is on its way the row's Delete and Rename are disabled, so a second press sends nothing.
    expect(within(rows()[0]).getByRole("button", { name: "Delete" }).getAttribute("aria-disabled")).toBe("true");
    expect(within(rows()[0]).getByRole("button", { name: "Rename" }).getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Delete" }));
    expect(onRemove).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveDelete();
    });
    expect(notices().some((t) => t.includes("Could not delete the bookmark"))).toBe(false);
  });
});

describe("Rename", () => {
  it("turns the label cell into a focused input of at most 100 characters; Enter saves through relabel", async () => {
    const { props } = draw([b({ id: "bmk_1", label: "Old" })]);
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Rename" }));
    const input = (await screen.findByRole("textbox")) as HTMLInputElement;
    expect(input.maxLength).toBe(LABEL_MAX);
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("Old");
    fireEvent.change(input, { target: { value: "Intro" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(props.onRelabel).toHaveBeenCalledWith("bmk_1", "Intro"));
  });

  it("Escape cancels the edit without saving", async () => {
    const { props } = draw([b({ id: "bmk_1", label: "Old" })]);
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Rename" }));
    const input = (await screen.findByRole("textbox")) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Changed" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(props.onRelabel).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
    expect(within(rows()[0]).getByText("Old")).toBeTruthy();
  });

  it("a refused relabel keeps the input open and says the save-failure words", async () => {
    const onRelabel = vi.fn(async () => {
      throw new ApiError(500, "internal", {}, 0);
    });
    draw([b({ id: "bmk_1", label: "Old" })], { onRelabel });
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Rename" }));
    const input = (await screen.findByRole("textbox")) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Changed" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(notices().some((t) => t.includes("Could not save the bookmark"))).toBe(true));
    expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe("Changed");
  });

  it("a 400 shows the server's own message and keeps the input open", async () => {
    const onRelabel = vi.fn(async () => {
      throw new ApiError(400, "invalid_request", { field: "label" }, 0, "label: must not contain control characters");
    });
    draw([b({ id: "bmk_1", label: "Old" })], { onRelabel });
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Rename" }));
    const input = (await screen.findByRole("textbox")) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Bad\u0001" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(notices().some((t) => t.includes("label: must not contain control characters"))).toBe(true));
    expect(screen.getByRole("textbox")).toBeTruthy();
  });

  it("two Enters while a save is in flight send one relabel", async () => {
    let resolveSave: (v: Bookmark) => void = () => {};
    const onRelabel = vi.fn(() => new Promise<Bookmark>((res) => { resolveSave = res; }));
    draw([b({ id: "bmk_1", label: "Old" })], { onRelabel });
    fireEvent.click(within(rows()[0]).getByRole("button", { name: "Rename" }));
    const input = (await screen.findByRole("textbox")) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Intro" } });
    fireEvent.keyDown(input, { key: "Enter" });
    fireEvent.keyUp(input, { key: "Enter" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onRelabel).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveSave(b({ label: "Intro" }));
    });
    await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
  });
});

describe("the empty state and a failed read", () => {
  it("shows the empty words and an add action that adds", () => {
    const { props } = draw([]);
    expect(screen.getByText("No bookmarks yet")).toBeTruthy();
    expect(screen.getByText("Add a bookmark to come back to a place in this file.")).toBeTruthy();
    // The empty state's own action (the footer keeps one with the same words).
    fireEvent.click(screen.getAllByRole("button", { name: "Add a bookmark here" })[0]);
    expect(props.marks.add).toHaveBeenCalledTimes(1);
  });

  it("shows the read-failure words with Retry instead of the table", () => {
    const { props } = draw([b()], { readFailed: true });
    expect(screen.getByText("Could not read the bookmarks")).toBeTruthy();
    expect(screen.queryByTestId("bookmark-row")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(props.onRetry).toHaveBeenCalledTimes(1);
  });

  it("the footer's add is disabled when full or with no element", () => {
    const full = draw([b()], { marks: { full: true } });
    const footerAdd = full.getAllByRole("button", { name: "Add a bookmark here" }).at(-1) as HTMLButtonElement;
    expect(footerAdd.disabled).toBe(true);
    full.unmount();
    const noEl = draw([b()], { media: null });
    const add = noEl.getAllByRole("button", { name: "Add a bookmark here" }).at(-1) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
  });
});
