// SPDX-License-Identifier: AGPL-3.0-or-later

// The generic file picker (slice pk1): the spaces first, startAt opened and read at once, a place's directories then the
// accepted files (others hidden), a single choice of a file leaf only, multiple checkboxes on files alone with the order of
// checking kept, the count in the submit button, the cap's note, Load more, the empty note, an error, and an answer that
// arrives after the session changed dropped quietly. childrenPage is replaced; the tree and the words are the real ones.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/api";
import { kindOf } from "../../lib/files/kind";
import type { FolderAccess, Node, Page } from "../../lib/files/types";
import { session, type Session } from "../../lib/session";

const childrenPage = vi.fn();
vi.mock("../../lib/files/api", () => ({
  childrenPage: (...args: unknown[]) => childrenPage(...args),
}));

import PickFiles from "./PickFiles";

const space = (id: string, name: string, kind: FolderAccess["kind"] = "team"): FolderAccess => ({
  id,
  name,
  kind,
  access: "write",
  role: "manager",
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: false,
});

function node(id: string, name: string, kind: Node["kind"], mime?: string): Node {
  return {
    id,
    folder_id: "f1",
    parent_id: null,
    name,
    kind,
    size: 1,
    mime_type: mime,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    no_download: false,
    editors_can_share: false,
  };
}

const song = node("a1", "song.mp3", "file", "audio/mpeg");
const clip = node("v1", "clip.webm", "file", "video/webm");
const text = node("t1", "notes.txt", "file", "text/plain");
const shortcut = { ...node("sc1", "short", "shortcut", "audio/mpeg"), target: { id: "a1", kind: "file" as const } };
const plans = node("d1", "Plans", "dir");
const sub = node("d2", "Sub", "dir");
const deep = node("a2", "deep.mp3", "file", "audio/mpeg");
const late = node("a4", "late.mp3", "file", "audio/mpeg");
const mine = node("a3", "mine.mp3", "file", "audio/mpeg");
const readme = node("t2", "readme.txt", "file", "text/plain");

const spaces = [space("f1", "Team A"), space("f2", "Empty team"), space("f3", "My space", "personal")];

// The accept the Playlists tab passes: audio and video only.
const accept = (n: Node) => kindOf(n) === "audio" || kindOf(n) === "video";

function listings(folder: string, dir: string | null, cursor?: string): Page<Node> {
  if (folder === "f1" && dir === null && cursor === "c1") return { items: [late] };
  if (folder === "f1" && dir === null) return { items: [plans, song, clip, text, shortcut], next_cursor: "c1" };
  if (folder === "f1" && dir === "d1") return { items: [sub, deep] };
  if (folder === "f2" && dir === null) return { items: [readme] };
  if (folder === "f3" && dir === null) return { items: [mine] };
  return { items: [] };
}

const rowOf = (name: string) => screen.getByText(name).closest(".ant-tree-treenode") as HTMLElement;
const openPlace = (name: string) => fireEvent.click(rowOf(name).querySelector(".ant-tree-switcher") as Element);
const checkBox = (name: string) => fireEvent.click(rowOf(name).querySelector(".ant-tree-checkbox") as Element);
const submit = () => screen.getByRole("button", { name: /^Add/ }) as HTMLButtonElement;

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

beforeEach(() => {
  childrenPage.mockReset();
  childrenPage.mockImplementation(async (folder: string, dir: string | null, cursor?: string) => listings(folder, dir, cursor));
});
afterEach(() => {
  session.set(null);
});

describe("PickFiles", () => {
  it("lists the spaces first and reads nothing without startAt", () => {
    render(<PickFiles title="Add files" action="Add" spaces={spaces} accept={accept} onPick={() => {}} onClose={() => {}} />);
    expect(screen.getByText("Team A")).toBeTruthy();
    expect(screen.getByText("Empty team")).toBeTruthy();
    expect(childrenPage).not.toHaveBeenCalled();
  });

  it("opens startAt at once and names the personal space My files", async () => {
    render(<PickFiles title="Add files" action="Add" spaces={spaces} startAt="f3" accept={accept} onPick={() => {}} onClose={() => {}} />);
    expect(await screen.findByText("mine.mp3")).toBeTruthy();
    expect(childrenPage).toHaveBeenCalledWith("f3", null, undefined);
    expect(childrenPage).toHaveBeenCalledTimes(1);
    expect(screen.getByText("My files")).toBeTruthy();
  });

  it("shows a place's directories then the accepted files, and hides the rest", async () => {
    render(<PickFiles title="Add files" action="Add" spaces={spaces} accept={accept} onPick={() => {}} onClose={() => {}} />);
    openPlace("Team A");
    expect(await screen.findByText("clip.webm")).toBeTruthy();
    expect(screen.getByText("Plans")).toBeTruthy();
    expect(screen.getByText("song.mp3")).toBeTruthy();
    expect(screen.queryByText("notes.txt")).toBeNull();
    expect(screen.queryByText("short")).toBeNull();
    const names = Array.from(document.querySelectorAll(".ant-tree-treenode")).map((r) => r.textContent);
    expect(names.indexOf("Plans")).toBeLessThan(names.indexOf("song.mp3"));
    expect(names.indexOf("song.mp3")).toBeLessThan(names.indexOf("clip.webm"));
    openPlace("Plans");
    expect(await screen.findByText("deep.mp3")).toBeTruthy();
    expect(screen.getByText("Sub")).toBeTruthy();
    expect(childrenPage).toHaveBeenCalledWith("f1", "d1", undefined);
  });

  it("single: a file leaf is chosen and submitted, a directory or space is not", async () => {
    const onPick = vi.fn();
    render(<PickFiles title="Choose a file" action="Choose" spaces={spaces} accept={accept} onPick={onPick} onClose={() => {}} />);
    openPlace("Team A");
    await screen.findByText("song.mp3");
    const choose = screen.getByRole("button", { name: "Choose" }) as HTMLButtonElement;
    expect(choose.disabled).toBe(true);
    fireEvent.click(screen.getByText("Plans"));
    expect(choose.disabled).toBe(true);
    fireEvent.click(screen.getByText("Team A"));
    expect(choose.disabled).toBe(true);
    fireEvent.click(screen.getByText("song.mp3"));
    expect(choose.disabled).toBe(false);
    fireEvent.click(choose);
    expect(onPick).toHaveBeenCalledTimes(1);
    expect(onPick).toHaveBeenCalledWith([song]);
  });

  it("multiple: only file leaves carry a checkbox, the order of checking is kept, and the count shows", async () => {
    const onPick = vi.fn();
    render(<PickFiles title="Add files" action="Add" spaces={spaces} accept={accept} multiple onPick={onPick} onClose={() => {}} />);
    openPlace("Team A");
    await screen.findByText("song.mp3");
    expect(rowOf("song.mp3").querySelector(".ant-tree-checkbox")).toBeTruthy();
    expect(rowOf("clip.webm").querySelector(".ant-tree-checkbox")).toBeTruthy();
    expect(rowOf("Plans").querySelector(".ant-tree-checkbox")).toBeNull();
    expect(rowOf("Team A").querySelector(".ant-tree-checkbox")).toBeNull();
    expect(rowOf("Empty team").querySelector(".ant-tree-checkbox")).toBeNull();
    expect(submit().disabled).toBe(true);
    expect(submit().textContent).toBe("Add (0)");
    checkBox("clip.webm");
    checkBox("song.mp3");
    expect(submit().textContent).toBe("Add (2)");
    expect(submit().disabled).toBe(false);
    // Unchecking and checking again moves the file to the end of the choice.
    checkBox("clip.webm");
    expect(submit().textContent).toBe("Add (1)");
    checkBox("clip.webm");
    fireEvent.click(submit());
    expect(onPick).toHaveBeenCalledTimes(1);
    expect(onPick).toHaveBeenCalledWith([song, clip]);
  });

  it("multiple: over max the submit stays off and the footer says the cap", async () => {
    const onPick = vi.fn();
    render(<PickFiles title="Add files" action="Add" spaces={spaces} accept={accept} multiple max={1} onPick={onPick} onClose={() => {}} />);
    openPlace("Team A");
    await screen.findByText("song.mp3");
    checkBox("song.mp3");
    expect(submit().disabled).toBe(false);
    checkBox("clip.webm");
    expect(submit().textContent).toBe("Add (2)");
    expect(submit().disabled).toBe(true);
    expect(screen.getByRole("note").textContent).toBe("At most 1 files");
    fireEvent.click(submit());
    expect(onPick).not.toHaveBeenCalled();
  });

  it("Load more reads the next page of the opened place", async () => {
    render(<PickFiles title="Add files" action="Add" spaces={spaces} accept={accept} multiple onPick={() => {}} onClose={() => {}} />);
    openPlace("Team A");
    await screen.findByText("song.mp3");
    const more = screen.getByRole("button", { name: "Load more" });
    fireEvent.click(more);
    expect(await screen.findByText("late.mp3")).toBeTruthy();
    expect(childrenPage).toHaveBeenCalledWith("f1", null, "c1");
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("an opened place with no directory and no accepted file says so", async () => {
    render(<PickFiles title="Add files" action="Add" spaces={spaces} accept={accept} onPick={() => {}} onClose={() => {}} />);
    openPlace("Empty team");
    expect(await screen.findByText("No matching files here")).toBeTruthy();
    expect(screen.queryByText("readme.txt")).toBeNull();
  });

  it("a failed read shows the error text", async () => {
    childrenPage.mockRejectedValue(new ApiError(403, "forbidden", {}, 0));
    render(<PickFiles title="Add files" action="Add" spaces={spaces} accept={accept} onPick={() => {}} onClose={() => {}} />);
    openPlace("Team A");
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByText("Your access does not allow this.")).toBeTruthy();
  });

  it("drops an answer that arrives after the session changed, and sends no read once it has", async () => {
    session.set(signedAs("olivia"));
    let answer!: (p: Page<Node>) => void;
    childrenPage.mockImplementation(() => new Promise<Page<Node>>((res) => (answer = res)));
    render(<PickFiles title="Add files" action="Add" spaces={spaces} startAt="f3" accept={accept} onPick={() => {}} onClose={() => {}} />);
    await waitFor(() => expect(childrenPage).toHaveBeenCalledTimes(1));
    session.set(null);
    await act(async () => {
      answer({ items: [mine] });
    });
    expect(screen.queryByText("mine.mp3")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    openPlace("Team A");
    await act(async () => {
      await Promise.resolve();
    });
    expect(childrenPage).toHaveBeenCalledTimes(1);
  });
});
