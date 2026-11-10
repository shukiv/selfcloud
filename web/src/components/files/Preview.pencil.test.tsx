// SPDX-License-Identifier: AGPL-3.0-or-later

import "../../test/rangeRects";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node, Role } from "../../lib/files/types";

// The pencil of a code file in the preview (decision 124): an editor sees it, a viewer does not, and a file over 2 MiB has none.
const signedLink = vi.fn();
vi.mock("../../lib/files/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/api")>()),
  signedLink: (...args: unknown[]) => signedLink(...args),
}));

import Preview from "./Preview";

afterEach(() => {
  vi.unstubAllGlobals();
});

const MiB = 1024 * 1024;

function file(name: string, role: Role, size: number): Node {
  return {
    id: `n-${name}`,
    folder_id: "f1",
    parent_id: null,
    name,
    kind: "file",
    size,
    mime_type: "application/json",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role,
    no_download: false,
    editors_can_share: false,
  };
}

// codeShown waits until the code viewer shows the file's text.
async function codeShown() {
  await waitFor(() => {
    if (!document.querySelector(".cm-content")) throw new Error("no code viewer yet");
  });
}

const pencil = () => screen.queryByRole("button", { name: "Open in editor" });

describe("the pencil of a code file in the preview", () => {
  it("is shown to an editor of a text file", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response('{"a":1}\n', { status: 206 })));
    render(<Preview items={[file("a.json", "editor", 8)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await codeShown();
    expect(pencil()).not.toBeNull();
  });

  it("is not shown to a viewer, who reads the file", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response('{"a":1}\n', { status: 206 })));
    render(<Preview items={[file("a.json", "viewer", 8)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await codeShown();
    expect(pencil()).toBeNull();
  });

  it("is not shown for a file over 2 MiB, which the editor does not take", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response('{"a":1}\n', { status: 206 })));
    render(<Preview items={[file("big.json", "editor", 3 * MiB)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await codeShown();
    expect(pencil()).toBeNull();
  });
});
