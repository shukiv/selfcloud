// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";

const filesApi = vi.hoisted(() => ({
  statNode: vi.fn(),
  childrenPage: vi.fn(),
  signedLink: vi.fn(),
}));
vi.mock("../../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../../lib/files/api")>()), ...filesApi }));

const sharingApi = vi.hoisted(() => ({
  visitorNode: vi.fn(),
  visitorChildren: vi.fn(),
  visitorLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...sharingApi }));

import PublicBrowse, { sessionSource, visitorSource } from "./PublicBrowse";

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

function textFile(id: string, name: string): Node {
  return { ...dirNode(id, name), kind: "file", mime_type: "text/plain", size: 10 } as unknown as Node;
}

afterEach(() => vi.clearAllMocks());

describe("PublicBrowse over the signed-in source", () => {
  it("reads the folder through the drive's own calls, and lists it the same way", async () => {
    const root = dirNode("nod_root", "Plans");
    const child = dirNode("nod_child", "Notes");
    filesApi.statNode.mockResolvedValue(root);
    filesApi.childrenPage.mockResolvedValue({ items: [child], next_cursor: null });
    render(<PublicBrowse token="" root="nod_root" source={sessionSource()} onlost={vi.fn()} />);
    expect(await screen.findByRole("heading", { name: "Plans" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Notes" })).toBeTruthy();
    expect(filesApi.statNode).toHaveBeenCalledWith("nod_root");
    expect(filesApi.childrenPage).toHaveBeenCalledWith("", "nod_root", undefined);
    expect(sharingApi.visitorNode).not.toHaveBeenCalled();
    expect(sharingApi.visitorChildren).not.toHaveBeenCalled();
  });

  it("a plain file asks its download link only when Download is clicked", async () => {
    const file = textFile("nod_file", "notes.txt");
    filesApi.statNode.mockResolvedValue(file);
    filesApi.signedLink.mockResolvedValue({ url: "https://content.example/notes", expires_at: new Date(Date.now() + 300_000).toISOString() });
    const clicked: string[] = [];
    const realClick = HTMLAnchorElement.prototype.click;
    HTMLAnchorElement.prototype.click = function () {
      clicked.push(this.href);
    };
    try {
      render(<PublicBrowse token="" root="nod_file" source={sessionSource()} onlost={vi.fn()} />);
      const button = await screen.findByRole("button", { name: "Download" });
      // Nothing was asked for the file's contents before the click.
      expect(filesApi.signedLink).not.toHaveBeenCalled();
      fireEvent.click(button);
      await waitFor(() => expect(filesApi.signedLink).toHaveBeenCalledWith("nod_file", "attachment", expect.any(Number)));
      expect(clicked).toEqual(["https://content.example/notes"]);
    } finally {
      HTMLAnchorElement.prototype.click = realClick;
    }
  });

  it("the visitor source reads through the visitor's calls with its token", async () => {
    const root = dirNode("nod_root", "Plans");
    sharingApi.visitorNode.mockResolvedValue(root);
    sharingApi.visitorChildren.mockResolvedValue({ items: [], next_cursor: null });
    render(<PublicBrowse token="tok_v" root="nod_root" source={visitorSource("tok_v")} onlost={vi.fn()} />);
    expect(await screen.findByRole("heading", { name: "Plans" })).toBeTruthy();
    expect(sharingApi.visitorNode).toHaveBeenCalledWith("tok_v", "nod_root");
    expect(filesApi.statNode).not.toHaveBeenCalled();
  });
});

describe("PublicBrowse with no source keeps the visitor's way", () => {
  it("a plain file's download link is asked for at open, as before", async () => {
    const file = textFile("nod_file", "notes.txt");
    sharingApi.visitorNode.mockResolvedValue(file);
    sharingApi.visitorLink.mockResolvedValue({ url: "https://content.example/notes", expires_at: new Date(Date.now() + 300_000).toISOString() });
    render(<PublicBrowse token="tok_v" root="nod_file" onlost={vi.fn()} />);
    expect(await screen.findByText("Download")).toBeTruthy();
    expect(sharingApi.visitorLink).toHaveBeenCalledWith("tok_v", "nod_file", "attachment");
    expect(filesApi.signedLink).not.toHaveBeenCalled();
  });
});