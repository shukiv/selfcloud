// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { nextRow, rowStep, type TilePlace } from "./grid";

// bands lays out the tile view on screen: the folders in one grid of `cols` columns, then the files in a grid of their own
// under it (a new row for the files, as the page does). The flat order is folders then files, as the listing's ids are.
function bands(folders: number, files: number, cols: number): TilePlace[] {
  const out: TilePlace[] = [];
  const COL = 272;
  const at = (i: number, top: number) => ({ top, x: (i % cols) * COL, width: 260 });
  const folderRows = Math.ceil(folders / cols);
  for (let i = 0; i < folders; i++) out.push(at(i, 100 + Math.floor(i / cols) * 52));
  const filesTop = 100 + folderRows * 52 + 24;
  for (let i = 0; i < files; i++) out.push(at(i, filesTop + Math.floor(i / cols) * 180));
  return out;
}

// mirror flips the columns, as a right-to-left page lays them out: the first tile is at the right.
function mirror(tiles: TilePlace[], width: number): TilePlace[] {
  return tiles.map((t) => ({ ...t, x: width - t.x - t.width }));
}

describe("nextRow: Up and Down across the folder band and the file band", () => {
  // Seven folders and five columns: a row of five and a partial row of two. Then twelve files: two full rows and a partial one.
  const tiles = bands(7, 12, 5);
  const FOLDER_ROW_1 = 0; // index 0..4
  const FOLDER_ROW_2 = 5; // index 5, 6
  const FILE_ROW_1 = 7; // index 7..11
  const FILE_ROW_2 = 12; // index 12..16
  const FILE_ROW_3 = 17; // index 17, 18

  it("moves Down from a folder in a full row to the folder below it", () => {
    expect(nextRow(tiles, FOLDER_ROW_1 + 1, 1)).toBe(FOLDER_ROW_2 + 1);
  });

  it("moves Down from a folder in the partial last folder row to the file of the same column in the band below", () => {
    // Folder 6 is column 1 of the partial row; the file band starts on a new row, its column 1 is file 8.
    expect(nextRow(tiles, 6, 1)).toBe(FILE_ROW_1 + 1);
  });

  it("moves Up from the first file row to the folder row above it, to the folder in the same column", () => {
    // File 9 is column 2 of the first file row; the last folder row has two tiles, so the nearest is folder 6 (column 1).
    expect(nextRow(tiles, FILE_ROW_1 + 2, -1)).toBe(FOLDER_ROW_2 + 1);
  });

  it("moves Up from a file in a column with no folder above it to the last folder of the row above", () => {
    // File 11 is column 4: the row above has columns 0 and 1 only, so the nearest there is folder 6.
    expect(nextRow(tiles, FILE_ROW_1 + 4, -1)).toBe(FOLDER_ROW_2 + 1);
  });

  it("moves Down through the file rows, and Up again to the same column", () => {
    expect(nextRow(tiles, FILE_ROW_1 + 1, 1)).toBe(FILE_ROW_2 + 1);
    expect(nextRow(tiles, FILE_ROW_2 + 1, 1)).toBe(FILE_ROW_3 + 1);
    expect(nextRow(tiles, FILE_ROW_3 + 1, -1)).toBe(FILE_ROW_2 + 1);
    expect(nextRow(tiles, FILE_ROW_2 + 1, -1)).toBe(FILE_ROW_1 + 1);
  });

  it("stays on the tile where there is no row that way", () => {
    expect(nextRow(tiles, FILE_ROW_3, 1)).toBe(FILE_ROW_3);
    expect(nextRow(tiles, 0, -1)).toBe(0);
  });

  it("follows the screen in a right-to-left layout: the column is the one on the screen", () => {
    const rtl = mirror(tiles, 1360);
    // Folder 6 is the second tile from the right of the partial row; file 8 is the second from the right of the band below.
    expect(nextRow(rtl, 6, 1)).toBe(FILE_ROW_1 + 1);
  });
});

describe("rowStep: the step the page moves by", () => {
  it("is the step to the tile one row away", () => {
    const tiles = bands(7, 12, 5);
    expect(rowStep(tiles, 1, 1)).toBe(5);
    expect(rowStep(tiles, 6, 1)).toBe(2);
  });

  it("is zero when a tile of the order is not on the screen: no column count stands in for it", () => {
    const tiles: (TilePlace | null)[] = bands(7, 12, 5);
    tiles[9] = null;
    expect(rowStep(tiles, 1, 1)).toBe(0);
    expect(rowStep(tiles, 8, -1)).toBe(0);
  });
});

describe("nextRow with one band only", () => {
  it("moves between the rows of files alone, with no folders", () => {
    const tiles = bands(0, 10, 4);
    expect(nextRow(tiles, 1, 1)).toBe(5);
    expect(nextRow(tiles, 9, -1)).toBe(5);
  });

  it("moves between the rows of folders alone, with no files", () => {
    const tiles = bands(9, 0, 4);
    expect(nextRow(tiles, 2, 1)).toBe(6);
    expect(nextRow(tiles, 8, -1)).toBe(4);
  });

  it("returns the same index for an index that is not a tile", () => {
    expect(nextRow([], 0, 1)).toBe(0);
  });
});
