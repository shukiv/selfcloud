// SPDX-License-Identifier: AGPL-3.0-or-later
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";

const api = vi.hoisted(() => ({
  visitorNode: vi.fn(),
  visitorChildren: vi.fn(),
  visitorLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import PublicBrowse from "./PublicBrowse";

function dirNode(id: string, name: string): Node {
  return {
    id,
    folder_id: "fld_a",
    parent_id: null,
    name,
    kind: "dir",
    size: 0,
    mime_type: "",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
  } as unknown as Node;
}

const deferred = <T,>() => {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
};

afterEach(() => vi.clearAllMocks());

describe("the folder the visitor opens", () => {
  it("the last click wins when the answers come back out of order (guard: the latest-request check in show)", async () => {
    const slow = deferred<{ items: Node[]; next_cursor: string | null }>();
    const root = dirNode("nod_root", "Plans");
    const slowDir = dirNode("nod_slow", "Slow");
    const fastDir = dirNode("nod_fast", "Fast");
    api.visitorNode.mockImplementation(async (_t: string, id: string) => (id === "nod_root" ? root : id === "nod_slow" ? slowDir : fastDir));
    api.visitorChildren.mockImplementation(async (_t: string, id: string) => {
      if (id === "nod_root") return { items: [slowDir, fastDir], next_cursor: null };
      if (id === "nod_slow") return slow.promise;
      return { items: [dirNode("nod_early", "Early")], next_cursor: null };
    });

    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Slow" }));
    fireEvent.click(screen.getByRole("button", { name: "Fast" }));

    expect(await screen.findByRole("heading", { name: "Fast" })).toBeTruthy();
    expect(await screen.findByText("Early")).toBeTruthy();

    await act(async () => {
      slow.resolve({ items: [dirNode("nod_late", "Late")], next_cursor: null });
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(screen.getByRole("heading", { name: "Fast" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Slow" })).toBeNull();
    expect(screen.queryByText("Late")).toBeNull();
    expect(screen.getByText("Early")).toBeTruthy();
  });
});

// A video on a share link plays like one in the owner's viewer: when its link has ended, the visitor's credential asks
// for a new one, and the player goes on from where it was.
describe("a video on a share link", () => {
  it("asks the visitor's credential for a new link once the first has ended", async () => {
    const clip = {
      id: "nod_clip",
      folder_id: "fld_a",
      parent_id: null,
      name: "clip.webm",
      kind: "file",
      size: 10,
      mime_type: "video/webm",
      created_at: "2026-10-01T00:00:00Z",
      modified_at: "2026-10-01T00:00:00Z",
      role: "viewer",
      no_download: true,
      editors_can_share: false,
    } as unknown as Node;
    api.visitorNode.mockResolvedValue(clip);
    const ended = new Date(Date.now() - 60_000).toISOString();
    const live = new Date(Date.now() + 300_000).toISOString();
    api.visitorLink.mockImplementation(async (_t: string, _id: string, disposition: string) => {
      if (disposition === "attachment") throw new Error("no download");
      const asked = api.visitorLink.mock.calls.filter((c) => c[2] === "inline").length;
      return asked === 1 ? { url: "https://content.example/first", expires_at: ended } : { url: "https://content.example/second", expires_at: live };
    });
    render(<PublicBrowse token="tok_v" root="nod_clip" onlost={vi.fn()} />);
    const video = await vi.waitFor(() => {
      const el = document.querySelector("video");
      if (!el) throw new Error("no video yet");
      return el;
    });
    expect(video.getAttribute("src")).toBe("https://content.example/first");
    Object.defineProperty(video, "error", { value: { code: 4 }, configurable: true });
    fireEvent.error(video);
    await vi.waitFor(() => expect(document.querySelector("video")?.getAttribute("src")).toBe("https://content.example/second"));
    expect(api.visitorLink.mock.calls.filter((c) => c[2] === "inline")).toHaveLength(2);
    expect(api.visitorLink).toHaveBeenCalledWith("tok_v", "nod_clip", "inline");
  });
});
