// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../api", () => ({ api: vi.fn(), ApiError: class ApiError extends Error {} }));

import { api } from "../api";
import { createDocument } from "./api";
import { newDocumentPath, openNewDocumentTab, readNewDocument } from "./documents";
import { documentKinds, myFilesPlace, placeForRoute, placeOfHere, type NewPlace } from "./newPlace";

const writable: NewPlace = {
  where: { folder: "fld_t", dirID: null, path: "/" },
  canFolder: true,
  canUpload: true,
  canUploadFolder: true,
  readOnly: false,
  canEdit: true,
  nodeID: "nod_root",
};

describe("New documents (decision 91)", () => {
  beforeEach(() => {
    vi.mocked(api).mockReset();
  });

  it("posts the kind to the folder's node, and reads the new file back", async () => {
    vi.mocked(api).mockResolvedValue({ id: "nod_new", name: "Untitled spreadsheet.xlsx" });
    const node = await createDocument("nod_root", "xlsx");
    expect(api).toHaveBeenCalledWith("POST", "/api/v1/nodes/nod_root/documents", { body: { type: "xlsx" } });
    expect(node.id).toBe("nod_new");
  });

  // The click opens the new-document page in a new tab, from the click itself, and makes nothing: the page makes the file
  // (decision 102, round 2 R5).
  it("the click opens /edit/new in a new tab with noopener, and the opener makes no file", () => {
    const open = vi.fn();
    const goTo = vi.fn();
    openNewDocumentTab({ parent: "nod_root", kind: "docx", from: "/files/fld_t", coarse: false, goTo }, open);
    expect(open).toHaveBeenCalledWith(newDocumentPath("nod_root", "docx", null, true), "_blank", "noopener");
    expect(goTo).not.toHaveBeenCalled();
    expect(api).not.toHaveBeenCalled();
  });

  it("on a coarse pointer the same page opens in this tab, with from set to the folder shown", () => {
    const open = vi.fn();
    const goTo = vi.fn();
    openNewDocumentTab({ parent: "nod_root", kind: "xlsx", from: "/files/fld_t", coarse: true, goTo }, open);
    expect(open).not.toHaveBeenCalled();
    expect(goTo).toHaveBeenCalledWith(newDocumentPath("nod_root", "xlsx", "/files/fld_t", false));
  });

  it("the new-document address carries the kind, the parent, the place to return to and the tab flag, and reads back", () => {
    const path = newDocumentPath("nod_root", "pptx", "/files/fld_t", true);
    expect(path.startsWith("/edit/new?")).toBe(true);
    expect(readNewDocument(path.split("?")[1] ?? "")).toEqual({ kind: "pptx", parent: "nod_root", from: "/files/fld_t", tab: true });
  });

  it("a kind that is not one of the three, or a parent that is not a node's ID, is no request", () => {
    expect(readNewDocument("kind=exe&parent=nod_root")).toBeNull();
    expect(readNewDocument("kind=docx&parent=../etc")).toBeNull();
    expect(readNewDocument("kind=docx")).toBeNull();
  });

  // The menu offers the kinds only while office editing is on, and in a place that takes new files with a node to hold them.
  it("offers the three kinds only with office editing on, in a place that takes new files", () => {
    expect(documentKinds(writable, true)).toEqual(["docx", "xlsx", "pptx"]);
    expect(documentKinds(writable, false)).toEqual([]);
    expect(documentKinds({ ...writable, readOnly: true, canUpload: false }, true)).toEqual([]);
    expect(documentKinds({ ...writable, canUpload: false }, true)).toEqual([]);
    expect(documentKinds({ ...writable, nodeID: null }, true)).toEqual([]);
    expect(documentKinds(null, true)).toEqual([]);
    // My files off the files pages has no node to hold a document, so it offers none.
    expect(documentKinds(myFilesPlace("fld_mine"), true)).toEqual([]);
  });

  it("a folder shown is the place's node: its directory, else the folder's root", () => {
    const here = { folder: "fld_t", folderName: "Team", folderRole: "manager", dir: null, dirRole: "manager", trail: [],
      rootReachable: true, reach: [], spaceNoDownload: false, path: "/", rootNodeID: "nod_root" };
    expect(placeOfHere(here as unknown as Parameters<typeof placeOfHere>[0], true, true).nodeID).toBe("nod_root");
    const inDir = { ...here, dir: { id: "nod_dir", name: "Sub", kind: "dir" } };
    expect(placeOfHere(inDir as unknown as Parameters<typeof placeOfHere>[0], true, true).nodeID).toBe("nod_dir");
  });
});

describe("the place a New document goes in must be the page's own (race, round 2)", () => {
  const shown = (folder: string, dirID: string | null): NewPlace => ({
    where: { folder, dirID, path: "/" },
    canFolder: true,
    canUpload: true,
    canUploadFolder: true,
    readOnly: false,
    canEdit: true,
    nodeID: dirID ?? "nod_roottttt",
  });

  it("a place from another folder than the route is not used", () => {
    expect(placeForRoute(shown("fld_otherrrr", null), "/files/fld_tttttttt")).toBeNull();
    expect(documentKinds(placeForRoute(shown("fld_otherrrr", null), "/files/fld_tttttttt"), true)).toEqual([]);
  });

  it("a place for another directory of the same folder is not used", () => {
    expect(placeForRoute(shown("fld_tttttttt", "nod_aaaaaaaa"), "/files/fld_tttttttt/nod_bbbbbbbb")).toBeNull();
  });

  it("the place of the route shown is used", () => {
    const p = shown("fld_tttttttt", "nod_bbbbbbbb");
    expect(placeForRoute(p, "/files/fld_tttttttt/nod_bbbbbbbb")).toBe(p);
    const root = shown("fld_tttttttt", null);
    expect(placeForRoute(root, "/files/fld_tttttttt")).toBe(root);
  });

  it("/files redirects to the personal folder, so no place is used there", () => {
    expect(placeForRoute(myFilesPlace("fld_minenenn"), "/files")).toBeNull();
    expect(placeForRoute(myFilesPlace("fld_minenenn"), "/files/fld_tttttttt")).toBeNull();
  });

  it("/n/{nod} uses only the place whose directory is that node", () => {
    const p = shown("fld_tttttttt", "nod_bbbbbbbb");
    expect(placeForRoute(p, "/n/nod_bbbbbbbb")).toBe(p);
    expect(placeForRoute(p, "/n/nod_otherrrr")).toBeNull();
    const root = shown("fld_tttttttt", null);
    expect(placeForRoute(root, "/n/nod_roottttt")).toBe(root);
  });

  it("a null place gives null", () => {
    expect(placeForRoute(null, "/files/fld_tttttttt")).toBeNull();
  });
});

describe("the New menu offers documents to an editor or a manager only (decision 91, round 2)", () => {
  const hereWith = (role: string, dirRole: string | null) =>
    ({
      folder: "fld_tttttttt",
      folderName: null,
      folderRole: role,
      dir: dirRole === null ? null : { id: "nod_dddddddd", name: "d", kind: "dir" },
      dirRole: dirRole ?? role,
      trail: [],
      rootReachable: true,
      reach: [],
      spaceNoDownload: false,
      path: "/d",
      rootNodeID: "nod_rootttttt",
    }) as unknown as Parameters<typeof placeOfHere>[0];

  it.each([
    ["viewer", true, false],
    ["commenter", true, false],
    ["contributor", true, false],
    ["editor", true, true],
    ["manager", true, true],
  ])("a %s of the folder gets documents: %s", (role, canUpload, want) => {
    const p = placeOfHere(hereWith(role, null), canUpload, canUpload);
    expect(documentKinds(p, true).length > 0).toBe(want);
  });

  it("the directory shown decides: an editor of the folder who is only a contributor of the directory gets none", () => {
    expect(documentKinds(placeOfHere(hereWith("editor", "contributor"), true, true), true)).toEqual([]);
    expect(documentKinds(placeOfHere(hereWith("contributor", "manager"), true, true), true)).toEqual(["docx", "xlsx", "pptx"]);
  });

  it("a place that is not an editor's offers none, whatever else it allows", () => {
    expect(documentKinds({ ...writable, canEdit: false }, true)).toEqual([]);
  });
});

describe("no documents while an installation admin is signed in as a user (decision 91, round 2)", () => {
  it("the menu offers none while impersonating, even in a folder the person edits", () => {
    expect(documentKinds(writable, true, true)).toEqual([]);
    expect(documentKinds(writable, true, false)).toEqual(["docx", "xlsx", "pptx"]);
  });
});

