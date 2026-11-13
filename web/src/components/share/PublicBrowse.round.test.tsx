// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page: a no-download clip plays on a share (as it does on the owner's viewer), and shows no
// Download button; and the heading names the file the playlist is playing, not the file the visitor opened.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import type { Node } from "../../lib/files/types";

const api = vi.hoisted(() => ({
  visitorNode: vi.fn(),
  visitorChildren: vi.fn(),
  visitorLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import PublicBrowse from "./PublicBrowse";

const live = new Date(Date.now() + 300_000).toISOString();

function clip(id: string, extra: Partial<Node> = {}): Node {
  return {
    id,
    folder_id: "fld_a",
    parent_id: "nod_root",
    name: `${id.replace("nod_", "")}.webm`,
    kind: "file",
    size: 10,
    mime_type: "video/webm",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: true,
    editors_can_share: false,
    ...extra,
  } as unknown as Node;
}

// The page loads its player as a lazy chunk, and the first render of the player is the slowest. Playing one clip here, once,
// keeps the first test from paying for both inside its own waits, which a busy machine stretches past them.
beforeAll(async () => {
  api.visitorNode.mockResolvedValue(clip("nod_warm"));
  api.visitorLink.mockResolvedValue({ url: "https://content.example/nod_warm", expires_at: live });
  const warm = render(<PublicBrowse token="tok_v" root="nod_warm" onlost={vi.fn()} />);
  await vi.waitFor(() => expect(document.querySelector("video")).not.toBeNull(), { timeout: 60_000 });
  warm.unmount();
  api.visitorNode.mockReset();
  api.visitorLink.mockReset();
}, 120_000);

afterEach(() => vi.clearAllMocks());

describe("a no-download clip on a share", () => {
  it("plays, and shows no Download button", async () => {
    const c = clip("nod_clip");
    api.visitorNode.mockResolvedValue(c);
    api.visitorLink.mockImplementation(async (_t: string, id: string, disposition: string) => {
      if (disposition === "attachment") {
        const { ApiError } = await import("../../lib/apiError");
        throw new ApiError(403, "download_restricted", {}, 0);
      }
      return { url: `https://content.example/${id}`, expires_at: live };
    });
    render(<PublicBrowse token="tok_v" root="nod_clip" onlost={vi.fn()} />);
    const video = await vi.waitFor(() => {
      const el = document.querySelector("video");
      if (!el) throw new Error("no video yet");
      return el;
    }, { timeout: 5000 });
    expect(video.getAttribute("src")).toBe("https://content.example/nod_clip");
    expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
  });
});

// A share's playing file shows the server's answer for its download: the button when the server allows it, and the
// downloads-off notice with no button when it refuses. Both ways the refusal can come are tested: an explicit no-download
// flag on the file, and an inherited one the page cannot see in the file (only the server's refusal tells).
describe("the download of a playing file on a share", () => {
  const refusedAttachment = async (_t: string, id: string, disposition: string) => {
    if (disposition === "attachment") throw new ApiError(403, "download_restricted", {}, 0);
    return { url: `https://content.example/${id}`, expires_at: live };
  };

  async function playClip(node: Node) {
    api.visitorNode.mockResolvedValue(node);
    render(<PublicBrowse token="tok_v" root={node.id} onlost={vi.fn()} />);
    await vi.waitFor(() => expect(document.querySelector("video")).not.toBeNull(), { timeout: 5000 });
  }

  it("an explicit no-download file shows the notice and no Download button", async () => {
    api.visitorLink.mockImplementation(refusedAttachment);
    await playClip(clip("nod_explicit", { no_download: true, role: "viewer" }));
    expect(await screen.findByText("Downloads of this file are turned off.", undefined, { timeout: 5000 })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
  });

  it("an inherited no-download flag the file does not show gets the same notice, not a Download button that fails", async () => {
    api.visitorLink.mockImplementation(refusedAttachment);
    await playClip(clip("nod_inherited", { no_download: false, role: "viewer" }));
    expect(await screen.findByText("Downloads of this file are turned off.", undefined, { timeout: 5000 })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
  });
});

describe("the heading on a share", () => {
  it("never names the previous file while another video opens", async () => {
    const root = { ...clip("nod_root", { kind: "dir", mime_type: "" }), name: "Folder" } as unknown as Node;
    const a = clip("nod_a", { no_download: false });
    const b = clip("nod_b", { no_download: false });
    api.visitorNode.mockImplementation(async (_t: string, id: string) => (id === "nod_root" ? root : id === "nod_a" ? a : b));
    api.visitorChildren.mockResolvedValue({ items: [a, b], next_cursor: null });
    api.visitorLink.mockImplementation(async (_t: string, id: string) => ({ url: `https://content.example/${id}`, expires_at: live }));
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "a.webm" }));
    await vi.waitFor(() => expect(document.querySelector("video")?.getAttribute("src")).toBe("https://content.example/nod_a"), { timeout: 5000 });
    // Back to the folder, then the other video: record every heading the page shows while it opens.
    fireEvent.click(screen.getByRole("button", { name: "Folder" }));
    await screen.findByRole("button", { name: "b.webm" });
    const seen: string[] = [];
    const heading = () => document.querySelector("h1")?.textContent ?? "";
    seen.push(heading());
    const watch = new MutationObserver(() => seen.push(heading()));
    watch.observe(document.body, { subtree: true, childList: true, characterData: true });
    fireEvent.click(screen.getByRole("button", { name: "b.webm" }));
    await vi.waitFor(() => expect(document.querySelector("video")?.getAttribute("src")).toBe("https://content.example/nod_b"), { timeout: 5000 });
    watch.disconnect();
    expect(seen).not.toContain("a.webm");
  });
});

describe("the heading on a share, playing", () => {
  it("names the file the playlist is playing as it moves on", async () => {
    const root = { ...clip("nod_root", { kind: "dir", mime_type: "" }), name: "Folder" } as unknown as Node;
    const a = clip("nod_a");
    const b = clip("nod_b");
    api.visitorNode.mockImplementation(async (_t: string, id: string) => (id === "nod_root" ? root : id === "nod_a" ? a : b));
    api.visitorChildren.mockResolvedValue({ items: [a, b], next_cursor: null });
    api.visitorLink.mockImplementation(async (_t: string, id: string) => ({ url: `https://content.example/${id}`, expires_at: live }));
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "a.webm" }));
    const video = await vi.waitFor(() => {
      const el = document.querySelector("video");
      if (!el || el.getAttribute("src") !== "https://content.example/nod_a") throw new Error("a not playing yet");
      return el;
    }, { timeout: 5000 });
    expect(screen.getByRole("heading", { name: "a.webm" })).toBeTruthy();
    fireEvent.ended(video);
    await vi.waitFor(() => expect(document.querySelector("video")?.getAttribute("src")).toBe("https://content.example/nod_b"), { timeout: 5000 });
    expect(screen.getByRole("heading", { name: "b.webm" })).toBeTruthy();
  });
});
