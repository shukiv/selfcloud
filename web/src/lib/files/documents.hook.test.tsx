// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { App as AntApp } from "antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useNewDocument } from "./documents";
import { createDocument } from "./api";
import { session } from "../session";

vi.mock("./api", () => ({ createDocument: vi.fn(() => new Promise(() => {})) }));

// popup is what window.open returns for a tab the browser let open: a blocked pop-up returns null instead.
const popup = () => ({ opener: {} }) as never;
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

// opened is the address each new tab was opened at, read from the window's open calls.
function opened(spy: { mock: { calls: unknown[][] } }): string[] {
  return spy.mock.calls.map((c) => String(c[0]));
}

describe("a New document goes where the page shows, never to a stale place (race, round 2 R5)", () => {
  beforeEach(() => {
    newPlace.set(null);
    vi.restoreAllMocks();
  });

  it("a place of folder A is not used while the route shows folder B's directory: no tab opens for it", () => {
    const spy = vi.spyOn(window, "open").mockImplementation(() => null);
    newPlace.set(placeOf(folderA, dirA));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<QueryClientProvider client={new QueryClient()}><RouterProvider router={router} /></QueryClientProvider>);
    fireEvent.click(screen.getByText("pick"));
    expect(spy).not.toHaveBeenCalled();
    expect(opened(spy).join(" ")).not.toContain(dirA);
  });
});

describe("the pick opens the new-document page for the directory shown (round 2 R5)", () => {
  beforeEach(() => {
    newPlace.set(null);
    vi.restoreAllMocks();
  });

  it("the pick opens the wait page in a new tab, makes the file in the directory shown, and the opener does not move", () => {
    const spy = vi.spyOn(window, "open").mockImplementation(popup);
    const dir = { id: dirB, name: "d", kind: "dir" } as never;
    newPlace.set(placeOfHere({ folder: folderB, folderName: null, folderRole: "contributor", dir, dirRole: "contributor", trail: [], rootReachable: true, reach: [], spaceNoDownload: false, path: "/d" } as never, true, true));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<QueryClientProvider client={new QueryClient()}><RouterProvider router={router} /></QueryClientProvider>);
    fireEvent.click(screen.getByText("pick"));
    expect(spy).toHaveBeenCalledTimes(1);
    const [url, target, features] = spy.mock.calls[0] as [string, string, string];
    expect(url).toMatch(/^\/edit\/wait\?k=[A-Za-z0-9_-]{22}$/);
    expect(url).not.toContain(dirB);
    expect(target).toBe("_blank");
    expect(features).toBeUndefined();
    expect(createDocument).toHaveBeenCalledWith(dirB, "docx");
    expect(router.state.location.pathname).toBe(`/files/${folderB}/${dirB}`);
  });
});

describe("the opener refreshes the listing its own create changed (decision 102, round 2 R5 fix)", () => {
  beforeEach(() => {
    newPlace.set(null);
    vi.restoreAllMocks();
  });

  it("after the create succeeds, the folder's listing is invalidated, so the new row is there when the person comes back", async () => {
    vi.mocked(createDocument).mockResolvedValueOnce({ id: "nod_new", name: "Untitled document.docx" } as never);
    const spy = vi.spyOn(window, "open").mockImplementation(popup);
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const dir = { id: dirB, name: "d", kind: "dir" } as never;
    newPlace.set(placeOfHere({ folder: folderB, folderName: null, folderRole: "contributor", dir, dirRole: "contributor", trail: [], rootReachable: true, reach: [], spaceNoDownload: false, path: "/d" } as never, true, true));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<QueryClientProvider client={qc}><RouterProvider router={router} /></QueryClientProvider>);
    fireEvent.click(screen.getByText("pick"));
    expect(spy).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(invalidate).toHaveBeenCalled());
    const keys = invalidate.mock.calls.map((c) => JSON.stringify((c[0] as { queryKey: unknown[] }).queryKey));
    expect(keys.some((k) => k.includes('"children"'))).toBe(true);
  });

  it("a create that fails invalidates nothing", async () => {
    vi.mocked(createDocument).mockRejectedValueOnce(new Error("over quota"));
    vi.spyOn(window, "open").mockImplementation(popup);
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const dir = { id: dirB, name: "d", kind: "dir" } as never;
    newPlace.set(placeOfHere({ folder: folderB, folderName: null, folderRole: "contributor", dir, dirRole: "contributor", trail: [], rootReachable: true, reach: [], spaceNoDownload: false, path: "/d" } as never, true, true));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<QueryClientProvider client={qc}><RouterProvider router={router} /></QueryClientProvider>);
    fireEvent.click(screen.getByText("pick"));
    await new Promise((r) => setTimeout(r, 20));
    expect(invalidate).not.toHaveBeenCalled();
  });
});

// mountPick renders the pick in the directory shown, with the place the files page writes for it.
function mountPick() {
  const dir = { id: dirB, name: "d", kind: "dir" } as never;
  newPlace.set(placeOfHere({ folder: folderB, folderName: null, folderRole: "contributor", dir, dirRole: "contributor", trail: [], rootReachable: true, reach: [], spaceNoDownload: false, path: "/d" } as never, true, true));
  const router = createMemoryRouter(
    [{ path: "*", element: <AntApp><Pick /></AntApp> }],
    { initialEntries: [`/files/${folderB}/${dirB}`] },
  );
  render(<QueryClientProvider client={new QueryClient()}><RouterProvider router={router} /></QueryClientProvider>);
}

describe("the New document is not offered while an installation admin is signed in as a person (review, round 2)", () => {
  beforeEach(() => {
    newPlace.set(null);
    vi.restoreAllMocks();
    vi.mocked(createDocument).mockClear();
  });

  it("a pick while impersonating opens no tab and makes no file", async () => {
    session.set({ user: { id: "usr_a", role: "user" }, impersonation: { endsAt: "2099-01-01T00:00:00Z" } } as never);
    const spy = vi.spyOn(window, "open").mockImplementation(popup);
    mountPick();
    fireEvent.click(screen.getByText("pick"));
    await new Promise((r) => setTimeout(r, 20));
    expect(spy).not.toHaveBeenCalled();
    expect(createDocument).not.toHaveBeenCalled();
    session.set(null);
  });
});

describe("a blocked new tab is said so, and makes no file (review, round 2)", () => {
  beforeEach(() => {
    newPlace.set(null);
    vi.restoreAllMocks();
    vi.mocked(createDocument).mockClear();
  });

  it("the browser's refusal to open the tab shows a message that says how to allow it, and no file is made", async () => {
    const spy = vi.spyOn(window, "open").mockImplementation(() => null);
    mountPick();
    fireEvent.click(screen.getByText("pick"));
    expect(spy).toHaveBeenCalledTimes(1);
    expect(await screen.findByText(/blocked the new tab/)).toBeTruthy();
    expect(createDocument).not.toHaveBeenCalled();
  });
});
