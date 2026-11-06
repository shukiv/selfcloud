// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../api", () => ({ api: vi.fn(), ApiError: class ApiError extends Error {} }));

import { api } from "../api";
import { createDocument } from "./api";
import { editorPath } from "./editor";
import { openNewDocument } from "./documents";
import { documentKinds, myFilesPlace, placeForRoute, placeOfHere, type NewPlace } from "./newPlace";
import type { Node } from "./types";

const writable: NewPlace = {
  where: { folder: "fld_t", dirID: null, path: "/" },
  canFolder: true,
  canUpload: true,
  canUploadFolder: true,
  readOnly: false,
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

  it("creates the document, then opens it in the editor in this tab, back to the folder shown", async () => {
    const node = { id: "nod_new", name: "Untitled document.docx" } as Node;
    const create = vi.fn().mockResolvedValue(node);
    const navigate = vi.fn();
    const onCreated = vi.fn();
    const onError = vi.fn();
    await openNewDocument({ nodeID: "nod_root", kind: "docx", from: "/files/fld_t", create, navigate, onCreated, onError });
    expect(create).toHaveBeenCalledWith("nod_root", "docx");
    expect(onCreated).toHaveBeenCalledWith(node);
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith(editorPath("nod_new", "edit", "/files/fld_t"));
    expect(navigate.mock.calls[0][0]).toMatch(/^\/edit\/nod_new\?/);
    expect(onError).not.toHaveBeenCalled();
  });

  it("a failure is reported, and nothing is opened", async () => {
    const failure = new Error("over quota");
    const create = vi.fn().mockRejectedValue(failure);
    const navigate = vi.fn();
    const onCreated = vi.fn();
    const onError = vi.fn();
    await openNewDocument({ nodeID: "nod_root", kind: "pptx", from: "/files/fld_t", create, navigate, onCreated, onError });
    expect(onError).toHaveBeenCalledWith(failure);
    expect(navigate).not.toHaveBeenCalled();
    expect(onCreated).not.toHaveBeenCalled();
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
