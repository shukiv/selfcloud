// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { App as AntApp } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

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
    render(<RouterProvider router={router} />);
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

  it("the pick opens /edit/new for the directory shown, in a new tab with noopener, and the opener does not move", () => {
    const spy = vi.spyOn(window, "open").mockImplementation(() => null);
    const dir = { id: dirB, name: "d", kind: "dir" } as never;
    newPlace.set(placeOfHere({ folder: folderB, folderName: null, folderRole: "contributor", dir, dirRole: "contributor", trail: [], rootReachable: true, reach: [], spaceNoDownload: false, path: "/d" } as never, true, true));
    const router = createMemoryRouter(
      [{ path: "*", element: <AntApp><Pick /></AntApp> }],
      { initialEntries: [`/files/${folderB}/${dirB}`] },
    );
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByText("pick"));
    expect(spy).toHaveBeenCalledTimes(1);
    const [url, target, features] = spy.mock.calls[0] as [string, string, string];
    expect(url.startsWith("/edit/new?")).toBe(true);
    expect(new URLSearchParams(url.split("?")[1]).get("parent")).toBe(dirB);
    expect(new URLSearchParams(url.split("?")[1]).get("kind")).toBe("docx");
    expect(target).toBe("_blank");
    expect(features).toBe("noopener");
    expect(router.state.location.pathname).toBe(`/files/${folderB}/${dirB}`);
  });
});
