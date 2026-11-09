// SPDX-License-Identifier: AGPL-3.0-or-later

// The gallery's parser (nt2). Kept apart from newRoute.test.ts, which the menu's newURL tests own.
import { describe, expect, it } from "vitest";
import { parseFilesPath } from "./route";
import { parseNewPath } from "./newRoute";

const FLD = "fld_abcdefgh";
const NOD = "nod_abcdefgh";

describe("parseNewPath: the gallery's addresses", () => {
  it("reads a folder's root with its kind", () => {
    expect(parseNewPath(`/new/${FLD}`, "?kind=document")).toEqual({ folder: FLD, dir: null, kind: "document" });
  });

  it("reads a directory of the folder with its kind", () => {
    expect(parseNewPath(`/new/${FLD}/${NOD}`, "?kind=spreadsheet")).toEqual({ folder: FLD, dir: NOD, kind: "spreadsheet" });
  });

  it("reads presentation, and ignores other query values", () => {
    expect(parseNewPath(`/new/${FLD}`, "?x=1&kind=presentation")).toEqual({ folder: FLD, dir: null, kind: "presentation" });
  });

  it("a missing kind is not a gallery address", () => {
    expect(parseNewPath(`/new/${FLD}`, "")).toBeNull();
    expect(parseNewPath(`/new/${FLD}/${NOD}`, "?x=1")).toBeNull();
  });

  it("an unknown kind is not a gallery address", () => {
    for (const q of ["?kind=drawing", "?kind=", "?kind=Document", "?kind=docx", "?kind=document,spreadsheet"]) {
      expect(parseNewPath(`/new/${FLD}`, q), q).toBeNull();
    }
  });

  it("an invalid ID is null, in either place", () => {
    const bad = [`/new/fld_x`, `/new/${NOD}`, `/new/${FLD}/${FLD}`, `/new/${FLD}/nod_ABCDEFGH`, `/new/${FLD}/${NOD}/extra`, `/new/${FLD}!`, `/new/`, `/new`, `/new/${FLD}/`];
    for (const p of bad) {
      expect(parseNewPath(p, "?kind=document"), p).toBeNull();
    }
  });

  it("other paths are not gallery addresses", () => {
    expect(parseNewPath(`/files/${FLD}`, "?kind=document")).toBeNull();
    expect(parseNewPath(`/n/${NOD}`, "?kind=document")).toBeNull();
  });
});

describe("/files stays as it was", () => {
  it("still reads a folder root and a directory, and not /new", () => {
    expect(parseFilesPath(`/files/${FLD}`)).toEqual({ kind: "folder", folder: FLD });
    expect(parseFilesPath(`/files/${FLD}/${NOD}`)).toEqual({ kind: "dir", folder: FLD, dir: NOD });
    expect(parseFilesPath(`/new/${FLD}`)).toBeNull();
  });
});
