// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page's heading, at the moment a playlist mounts for a new video. The playlist is replaced here by a stand-in that
// records the heading the page shows when it mounts, so the one render in which the page could name the previous file is seen.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useLayoutEffect } from "react";
import type { Node } from "../../lib/files/types";

const api = vi.hoisted(() => ({
  visitorNode: vi.fn(),
  visitorChildren: vi.fn(),
  visitorLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

const mounts = vi.hoisted(() => [] as string[]);
vi.mock("../files/MediaPlaylist", () => ({
  default: function StandIn({ start, onCurrent }: { start: Node; onCurrent?: (n: Node | null) => void }) {
    useLayoutEffect(() => {
      mounts.push(document.querySelector("h1")?.textContent ?? "");
      onCurrent?.(start);
      // The stand-in is not a player: it reports its file and nothing else.
    }, []);
    return <p>playlist of {start.name}</p>;
  },
}));

import PublicBrowse from "./PublicBrowse";

const live = new Date(Date.now() + 300_000).toISOString();

function clip(id: string, name: string): Node {
  return {
    id,
    folder_id: "fld_a",
    parent_id: "nod_root",
    name,
    kind: "file",
    size: 10,
    mime_type: "video/webm",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
  } as unknown as Node;
}

describe("the heading when another video opens", () => {
  it("names the new video at the moment its playlist mounts, not the previous one", async () => {
    const root = { ...clip("nod_root", "Folder"), kind: "dir", mime_type: "" } as unknown as Node;
    const a = clip("nod_a", "a.webm");
    const b = clip("nod_b", "b.webm");
    api.visitorNode.mockImplementation(async (_t: string, id: string) => (id === "nod_root" ? root : id === "nod_a" ? a : b));
    api.visitorChildren.mockResolvedValue({ items: [a, b], next_cursor: null });
    api.visitorLink.mockImplementation(async (_t: string, id: string) => ({ url: `https://content.example/${id}`, expires_at: live }));
    mounts.length = 0;
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "a.webm" }));
    await vi.waitFor(() => expect(mounts).toHaveLength(1), { timeout: 5000 });
    fireEvent.click(screen.getByRole("button", { name: "Folder" }));
    await screen.findByRole("button", { name: "b.webm" });
    fireEvent.click(screen.getByRole("button", { name: "b.webm" }));
    await vi.waitFor(() => expect(mounts).toHaveLength(2), { timeout: 5000 });
    expect(mounts[0]).toBe("a.webm");
    expect(mounts[1]).toBe("b.webm");
  });
});
