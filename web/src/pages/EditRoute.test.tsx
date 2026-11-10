// SPDX-License-Identifier: AGPL-3.0-or-later

import { render as rtlRender, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { Node } from "../lib/files/types";

// The two editors are replaced by markers: the test checks which one the address goes to.
vi.mock("./CodeEditPage", () => ({ default: () => <p>code editor</p> }));
vi.mock("./OfficeEditPage", () => ({ default: () => <p>office editor</p> }));

const statMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/files/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/files/api")>()),
  statNode: statMock,
}));

import EditRoute from "./EditRoute";

function node(name: string, mime: string, kind: Node["kind"] = "file"): Node {
  return {
    id: "nod_a1",
    folder_id: "fld_b",
    parent_id: null,
    name,
    kind,
    size: 4,
    mime_type: mime,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
  };
}

function at(path: string) {
  return rtlRender(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/edit/:nod" element={<EditRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("the /edit/ address", () => {
  it("opens a code file in the code editor", async () => {
    statMock.mockResolvedValue(node("a.json", "application/json"));
    at("/edit/nod_a1?mode=edit");
    expect(await screen.findByText("code editor")).toBeTruthy();
  });

  it("opens a file whose name is in the code list, stored with another type, in the code editor", async () => {
    statMock.mockResolvedValue(node("main.ts", "video/mp2t"));
    at("/edit/nod_a1?mode=edit");
    expect(await screen.findByText("code editor")).toBeTruthy();
  });

  it("opens an office file in the office editor, as before", async () => {
    statMock.mockResolvedValue(node("letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"));
    at("/edit/nod_a1?mode=edit");
    expect(await screen.findByText("office editor")).toBeTruthy();
  });

  it("opens a folder, or a file that the code list does not take, in the office editor, which refuses it", async () => {
    statMock.mockResolvedValue(node("dir", "", "dir"));
    at("/edit/nod_a1?mode=edit");
    expect(await screen.findByText("office editor")).toBeTruthy();
  });

  it("opens in the office editor when the file cannot be read, which shows its own failure", async () => {
    statMock.mockRejectedValue(new Error("gone"));
    at("/edit/nod_a1?mode=edit");
    await waitFor(() => expect(screen.getByText("office editor")).toBeTruthy());
  });
});
