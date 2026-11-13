// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { theme as antdTheme } from "antd";
import { TYPE_COLOURS, fileTypeOf } from "./iconType";
import { antdTokens } from "../../theme/theme";
import { DARK, LIGHT, PALETTE, SIDEBAR } from "../../theme/tokens";

// lum and ratio are the WCAG relative luminance and contrast ratio of two sRGB hex colours.
function lum(hex: string): number {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((x) => (x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
function ratio(a: string, b: string): number {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const f = (mime_type: string | undefined, name: string, kind: "file" | "dir" | "shortcut" = "file") => fileTypeOf({ kind, mime_type, name });

// rgba reads a CSS colour the design token gives (rgba(...) or #hex) as [r, g, b, alpha].
function rgba(css: string): [number, number, number, number] {
  const m = css.match(/rgba?\(([^)]+)\)/);
  if (m) {
    const [r, g, b, a = "1"] = m[1].split(",").map((x) => x.trim());
    return [Number(r), Number(g), Number(b), Number(a)];
  }
  const hex = css.replace("#", "");
  return [parseInt(hex.slice(0, 2), 16), parseInt(hex.slice(2, 4), 16), parseInt(hex.slice(4, 6), 16), 1];
}
// over is the colour a translucent fill paints on a ground: the fill's alpha over the ground, as a hex colour.
function over(fill: string, ground: string): string {
  const [r, g, b, a] = rgba(fill);
  const [R, G, B] = rgba(ground);
  return "#" + [r, g, b].map((c, i) => Math.round(c * a + [R, G, B][i] * (1 - a)).toString(16).padStart(2, "0")).join("");
}

describe("fileTypeOf (decision 68, colour by type)", () => {
  it("draws a folder grey, with the folder glyph", () => {
    expect(fileTypeOf({ kind: "dir", name: "Photos" })).toEqual({ colour: "grey", glyph: "folder" });
  });

  it("gives the red group to PDF, images, video and audio", () => {
    expect(f("application/pdf", "a.pdf")).toEqual({ colour: "red", glyph: "pdf" });
    expect(f("image/png", "a.png")).toEqual({ colour: "red", glyph: "image" });
    expect(f("video/mp4", "a.mp4")).toEqual({ colour: "red", glyph: "video" });
    expect(f("audio/ogg", "a.ogg")).toEqual({ colour: "red", glyph: "audio" });
  });

  it("gives text and word documents blue, spreadsheets green, presentations orange", () => {
    expect(f("text/plain", "n.txt")).toEqual({ colour: "blue", glyph: "document" });
    expect(f("application/vnd.openxmlformats-officedocument.wordprocessingml.document", "a.docx")).toEqual({ colour: "blue", glyph: "document" });
    expect(f("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "a.xlsx")).toEqual({ colour: "green", glyph: "spreadsheet" });
    expect(f("text/csv", "a.csv")).toEqual({ colour: "green", glyph: "spreadsheet" });
    expect(f("application/vnd.openxmlformats-officedocument.presentationml.presentation", "a.pptx")).toEqual({ colour: "orange", glyph: "presentation" });
  });

  it("gives archives and unknown files grey", () => {
    expect(f("application/zip", "a.zip")).toEqual({ colour: "grey", glyph: "archive" });
    expect(f("application/octet-stream", "a.bin")).toEqual({ colour: "grey", glyph: "file" });
    expect(f(undefined, "noext")).toEqual({ colour: "grey", glyph: "file" });
  });

  it("takes the extension for a generic media type, in any letter case", () => {
    expect(f("application/octet-stream", "Scan.PDF")).toEqual({ colour: "red", glyph: "pdf" });
    expect(f("application/octet-stream", "photo.JPG")).toEqual({ colour: "red", glyph: "image" });
    expect(f("application/octet-stream", "song.Mp3")).toEqual({ colour: "red", glyph: "audio" });
    expect(f("application/octet-stream", "clip.MP4")).toEqual({ colour: "red", glyph: "video" });
    expect(f("", "Report.DOCX")).toEqual({ colour: "blue", glyph: "document" });
    expect(f("application/octet-stream", "notes.txt")).toEqual({ colour: "blue", glyph: "document" });
  });

  it("takes the extension when the media type is missing or generic, and not otherwise", () => {
    expect(f("", "map.png")).toEqual({ colour: "red", glyph: "image" });
    expect(f(undefined, "a.pdf")).toEqual({ colour: "red", glyph: "pdf" });
    // A type the server names is the one the file is: a .pdf labelled as plain text is text.
    expect(f("text/plain", "a.pdf")).toEqual({ colour: "blue", glyph: "document" });
  });

  it("gives a CSV or TSV spreadsheet green, whatever its media type (a .csv sent as text/plain)", () => {
    expect(f("text/plain", "export.csv")).toEqual({ colour: "green", glyph: "spreadsheet" });
    expect(f("text/plain", "export.TSV")).toEqual({ colour: "green", glyph: "spreadsheet" });
    expect(f("text/tab-separated-values", "export.tsv")).toEqual({ colour: "green", glyph: "spreadsheet" });
    expect(f("application/octet-stream", "export.csv")).toEqual({ colour: "green", glyph: "spreadsheet" });
    expect(f("text/plain", "readme.txt")).toEqual({ colour: "blue", glyph: "document" });
  });

  it("gives a file with no extension and a generic type grey", () => {
    expect(f("application/octet-stream", "LICENSE")).toEqual({ colour: "grey", glyph: "file" });
    expect(f("", "Makefile")).toEqual({ colour: "grey", glyph: "file" });
  });

  it("takes the extension for a generic media type", () => {
    expect(f("application/octet-stream", "Report.DOCX")).toEqual({ colour: "blue", glyph: "document" });
    expect(f("application/octet-stream", "budget.xlsx")).toEqual({ colour: "green", glyph: "spreadsheet" });
    expect(f("application/octet-stream", "deck.pptx")).toEqual({ colour: "orange", glyph: "presentation" });
  });

  it("keeps a shortcut grey and file-like (it has its own outline icon)", () => {
    expect(f("application/pdf", "link", "shortcut")).toEqual({ colour: "grey", glyph: "file" });
  });

  it("keeps each colour at 3:1 against the page and the card, and the white glyph at 3:1, in both modes", () => {
    for (const [name, hex] of Object.entries(TYPE_COLOURS)) {
      for (const page of [LIGHT.page, LIGHT.card, DARK.page, DARK.card]) {
        expect(ratio(hex, page), `${name} against ${page}`).toBeGreaterThanOrEqual(3);
      }
      expect(ratio(hex, "#FFFFFF"), `white glyph on ${name}`).toBeGreaterThanOrEqual(3);
    }
  });

  // A square also sits on the tile's preview area (antd's colorFillQuaternary, a translucent fill over
  // the card) and on a selected row or tile (colorPrimaryBg). Both are measured in both modes, as the fill is painted on the
  // ground under it: a selected tile's preview is its fill over the selected tint.
  it("keeps each colour at 3:1 on the tile preview and the selected fill, in both modes", () => {
    for (const mode of ["light", "dark"] as const) {
      const p = PALETTE[mode];
      const tokens = antdTheme.getDesignToken(antdTokens(mode, p, SIDEBAR));
      const grounds: Record<string, string> = {
        "preview on the card": over(tokens.colorFillQuaternary, p.card),
        "preview on a selected tile": over(tokens.colorFillQuaternary, tokens.colorPrimaryBg),
        "the selected fill": tokens.colorPrimaryBg,
      };
      for (const [name, hex] of Object.entries(TYPE_COLOURS)) {
        for (const [where, ground] of Object.entries(grounds)) {
          expect(ratio(hex, ground), `${mode}: ${name} on ${where} (${ground})`).toBeGreaterThanOrEqual(3);
        }
      }
    }
  });
});
