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
