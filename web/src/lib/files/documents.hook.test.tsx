// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, fireEvent, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { App as AntApp } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({ createDocument: vi.fn() }));

import { createDocument } from "./api";
import { useNewDocument } from "./documents";
import { newPlace, placeOfHere, type NewPlace } from "./newPlace";

// Pick is a stand-in for the New menu's Document entry: it calls the same hook the menus call.
function Pick() {
  const makeDocument = useNewDocument();
  return <button onClick={() => makeDocument("docx")}>pick</button>;
}

const folderA = "fld_aaaaaaaa";
const folderB = "fld_bbbbbbbb";
const dirA = "nod_aaaaaaaa";
const dirB = "nod_bbbbbbbb";

// placeOf is the place FilesPage writes for a directory: the place a stale store value would hold.
function placeOf(folder: string, dir: string): NewPlace {
  return {
    where: { folder, dirID: dir, path: "/x" },
    canFolder: true,
    canUpload: true,
    canUploadFolder: true,
    readOnly: false,
    canEdit: true,
    nodeID: dir,
  };
}

describe("a New document goes where the page shows, never to a stale place (race)", () => {
  beforeEach(() => {
    vi.mocked(createDocument).mockReset();
    vi.mocked(createDocument).mockResolvedValue({ id: "nod_new", name: "Untitled document.docx" } as never);
    newPlace.set(null);
  });

  it("a place of folder A is not used while the route shows folder B's directory", async () => {
    newPlace.set(placeOf(folderA, dirA));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByText("pick"));
    await waitFor(() => expect(createDocument).toHaveBeenCalledTimes(0));
    const calls = vi.mocked(createDocument).mock.calls.map((c) => c[0]);
    expect(calls).not.toContain(dirA);
  });
});

describe("a place built the way FilesPage builds it is used on its own directory route", () => {
  beforeEach(() => {
    vi.mocked(createDocument).mockReset();
    vi.mocked(createDocument).mockResolvedValue({ id: "nod_new", name: "Untitled document.docx" } as never);
    newPlace.set(null);
  });

  it("the pick creates the document in the directory shown", async () => {
    const dir = { id: dirB, name: "d", kind: "dir" } as never;
    newPlace.set(placeOfHere({ folder: folderB, folderName: null, folderRole: "contributor", dir, dirRole: "contributor", trail: [], rootReachable: true, reach: [], spaceNoDownload: false, path: "/d" } as never, true, true));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByText("pick"));
    await waitFor(() => expect(createDocument).toHaveBeenCalledWith(dirB, "docx"));
  });
});

// deferred returns a promise and the function that settles it, so a test decides when the server answers.
function deferred() {
  let resolve!: (v: unknown) => void;
  const promise = new Promise((r) => (resolve = r));
  return { promise, resolve };
}

describe("a pick while a document is being made (L1, L2)", () => {
  beforeEach(() => {
    vi.mocked(createDocument).mockReset();
    newPlace.set(null);
  });

  it("L1: a second pick while the first is pending makes nothing", async () => {
    const d = deferred();
    vi.mocked(createDocument).mockReturnValue(d.promise as never);
    newPlace.set(placeOf(folderB, dirB));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByText("pick"));
    fireEvent.click(screen.getByText("pick"));
    await waitFor(() => expect(createDocument).toHaveBeenCalled());
    expect(vi.mocked(createDocument).mock.calls.length).toBe(1);
    d.resolve({ id: "nod_new", name: "Untitled document.docx" });
  });

  it("L2: the editor is not opened when the page has moved on before the file exists", async () => {
    const d = deferred();
    vi.mocked(createDocument).mockReturnValue(d.promise as never);
    newPlace.set(placeOf(folderB, dirB));
    const start = `/files/${folderB}/${dirB}`;
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [start] },
    );
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByText("pick"));
    await waitFor(() => expect(createDocument).toHaveBeenCalled());
    await act(async () => {
      await router.navigate("/files/fld_elsewhere1");
    });
    d.resolve({ id: "nod_new", name: "Untitled document.docx" });
    await act(async () => { await new Promise((r) => setTimeout(r, 100)); });
    expect(router.state.location.pathname).toBe("/files/fld_elsewhere1");
  });
});
