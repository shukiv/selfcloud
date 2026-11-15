// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { ReactElement, ReactNode } from "react";
import type { MenuProps } from "antd";
import { ChevronRight } from "lucide-react";
import { newMenuTree, newMenuItems, type NewMenuNode, type NewMenuOptions, KIND_INK } from "./newMenu";
import { newEntries, myFilesPlace, type NewPlace, type BlankKind } from "./newPlace";
import { PALETTE, SIDEBAR } from "../../theme/tokens";
import { antdTokens } from "../../theme/theme";
import { contrast } from "../brand";

const ALL: BlankKind[] = ["docx", "xlsx", "pptx", "odg", "txt"];
const writable: NewPlace = myFilesPlace("fld_mine");
const viewer: NewPlace = { where: { folder: "fld_t", dirID: null, path: "/" }, canFolder: false, canUpload: false, canUploadFolder: false, readOnly: true, canEdit: false };

function tree(p: NewPlace, blanks: BlankKind[] = ALL) {
  return newMenuTree({ entries: newEntries(p), blanks, hints: !p.readOnly });
}
// shape gives each top-level node as a short label, so the order is one readable line.
function shape(nodes: NewMenuNode[]): string[] {
  return nodes.map((n) => {
    switch (n.type) {
      case "action": return n.key;
      case "divider": return "|";
      case "submenu": return `${n.key}>[${n.children.map((c) => (c.type === "blank" ? `blank:${c.kind}` : c.type === "template" ? `template:${c.kind}` : c.type)).join(",")}]`;
      case "blank": return `blank:${n.kind}`;
      case "template": return `template:${n.kind}`;
    }
  });
}

describe("the New menu, one list for the sidebar and the files page", () => {
  it("has the groups in order: New folder | Upload files, Upload folder | Document, Spreadsheet, Presentation, More", () => {
    expect(shape(tree(writable))).toEqual([
      "newFolder",
      "|",
      "upload",
      "uploadFolder",
      "|",
      "document>[blank:docx,template:document]",
      "spreadsheet>[blank:xlsx,template:spreadsheet]",
      "presentation>[blank:pptx,template:presentation]",
      "more>[blank:odg,blank:txt]",
    ].map((s) => s));
  });

  it("each kind's submenu is Blank and From a template, in that order", () => {
    const kinds = tree(writable).filter((n): n is Extract<NewMenuNode, { type: "submenu" }> => n.type === "submenu" && n.key !== "more");
    expect(kinds.map((k) => k.key)).toEqual(["document", "spreadsheet", "presentation"]);
    for (const k of kinds) expect(k.children.map((c) => c.type)).toEqual(["blank", "template"]);
  });

  it("Drawing and Text file are direct items of More, with no submenu and no template", () => {
    const more = tree(writable).find((n) => n.type === "submenu" && n.key === "more");
    expect(more?.type === "submenu" && more.children.map((c) => c.type)).toEqual(["blank", "blank"]);
    expect(more?.type === "submenu" && more.children.map((c) => (c.type === "blank" ? c.kind : ""))).toEqual(["odg", "txt"]);
  });

  it("a place that offers no documents has no Document, Spreadsheet, Presentation or More, and no divider after the uploads", () => {
    expect(shape(tree(writable, []))).toEqual(["newFolder", "|", "upload", "uploadFolder"]);
  });

  it("a place that offers only Spreadsheet keeps its order and only its kind (no More: Drawing and Text file need no kind of its own)", () => {
    expect(shape(tree(writable, ["xlsx"]))).toEqual([
      "newFolder", "|", "upload", "uploadFolder", "|",
      "spreadsheet>[blank:xlsx,template:spreadsheet]",
    ]);
  });

  // With office editing off the server still makes the empty text file, so More holds Text file alone.
  it("with office editing off, More holds only Text file", () => {
    expect(shape(tree(writable, ["txt"]))).toEqual(["newFolder", "|", "upload", "uploadFolder", "|", "more>[blank:txt]"]);
  });

  it("a read-only place (a viewer) shows its three actions disabled and no chord hints", () => {
    const t = newMenuTree({ entries: newEntries(viewer), blanks: [], hints: false });
    const actions = t.filter((n): n is Extract<NewMenuNode, { type: "action" }> => n.type === "action");
    expect(actions.map((a) => a.enabled)).toEqual([false, false, false]);
    expect(actions.every((a) => a.chord === undefined)).toBe(true);
  });

  it("a writable place shows the chord letters on the actions and the blank documents, and no others", () => {
    const t = tree(writable);
    const chords = (nodes: NewMenuNode[]): string[] => nodes.flatMap((n) => (n.type === "action" || n.type === "blank" ? [n.chord ?? ""] : n.type === "submenu" ? chords(n.children) : []));
    expect(chords(t)).toEqual(["newFolder", "upload", "uploadFolder", "docx", "xlsx", "pptx", "", ""]);
  });

  it("the templates are never disabled by the chord rules and carry the gallery kind", () => {
    const tpl = tree(writable).flatMap((n) => (n.type === "submenu" && n.key !== "more" ? n.children.filter((c) => c.type === "template") : []));
    expect(tpl).toEqual([
      { type: "template", kind: "document" },
      { type: "template", kind: "spreadsheet" },
      { type: "template", kind: "presentation" },
    ]);
  });

  // Ink: each kind's icon colour is readable on the menu's ground in both themes (4.5:1, WCAG AA for text beside it).
  it.each(["document", "spreadsheet", "presentation", "drawing", "text"] as const)("the %s icon has 4.5:1 or more on the light and the dark menu ground", (kind) => {
    const light = String(antdTokens("light", PALETTE.light, SIDEBAR).token?.colorBgElevated);
    const dark = String(antdTokens("dark", PALETTE.dark, SIDEBAR).token?.colorBgElevated);
    expect(contrast(KIND_INK.light[kind], light)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(KIND_INK.dark[kind], dark)).toBeGreaterThanOrEqual(4.5);
  });
});

// The row height is the same for every row of the menu, submenu titles included (decision 108: 40 px, 44 on a coarse
// pointer). A submenu title with no height of its own is drawn at antd's 32 px, so this test pins it.
describe("the New menu's rows are the same height in every row", () => {
  const text = { newFolder: "New folder", upload: "Upload files", uploadFolder: "Upload folder", document: "Document", spreadsheet: "Spreadsheet", presentation: "Presentation", more: "More", drawing: "Drawing", textFile: "Text file", blank: "Blank", fromTemplate: "From a template", shortcut: "Shortcut" };
  function heights(coarse: boolean): (string | number | undefined)[] {
    const items = newMenuItems(tree(writable), { text, dark: false, coarse, phone: false, onRun: () => undefined });
    return items.flatMap((item) => (item && "type" in item && item.type === "divider" ? [] : [(item as { style?: { height?: number } }).style?.height]));
  }
  it("every top-level row is 40 px on a fine pointer, submenu titles too", () => {
    const h = heights(false);
    expect(h.length).toBeGreaterThan(0);
    expect(h.every((x) => x === 40)).toBe(true);
  });
  it("every top-level row is 44 px on a coarse pointer, submenu titles too", () => {
    const h = heights(true);
    expect(h.length).toBeGreaterThan(0);
    expect(h.every((x) => x === 44)).toBe(true);
  });
});

// Decision 185: on a phone the New menu's submenus slide in the same popup (the hook reads the children the items keep);
// on a coarse pointer the chord hints are gone from every row, and the keys stay named for a screen reader.
describe("the New menu's phone rows and touch hints (decision 185)", () => {
  const text = { newFolder: "New folder", upload: "Upload files", uploadFolder: "Upload folder", document: "Document", spreadsheet: "Spreadsheet", presentation: "Presentation", more: "More", drawing: "Drawing", textFile: "Text file", blank: "Blank", fromTemplate: "From a template", shortcut: "Shortcut" };
  const opts = (over: Partial<NewMenuOptions> = {}): NewMenuOptions => ({ text, dark: false, coarse: false, phone: false, onRun: () => undefined, ...over });
  type Item = NonNullable<MenuProps["items"]>[number];
  // Row is the shape these checks read off a menu item: antd's item union names no extra, label or aria-keyshortcuts on
  // every member (a divider has none), so the helpers read the rows through this one structural type. The assertions are
  // unchanged; only the type the helpers return is named.
  type Row = { key?: string | number; type?: string; label?: ReactNode; extra?: ReactElement; children?: Row[]; "aria-keyshortcuts"?: string };
  const childrenOf = (i: Item | Row): Row[] => (i && "children" in i && Array.isArray(i.children) ? (i.children as Row[]) : []);
  const rows = (list: (Item | Row)[]): Row[] => list.flatMap((i) => (i && "type" in i && i.type === "divider" ? [] : [i as Row]));
  const allRows = (list: (Item | Row)[]): Row[] => rows(list).flatMap((i) => [i, ...allRows(childrenOf(i))]);
  const find = (list: (Item | Row)[], key: string): Row => {
    const item = list.find((i) => i && "key" in i && i.key === key);
    if (!item) throw new Error(`no item ${key}`);
    return item as Row;
  };

  it("phone: false keeps the menu's keys and types, submenus included", () => {
    const items = newMenuItems(tree(writable), opts());
    const shapeOf = (list: Item[]): string[] => list.map((i) => {
      if (i && "type" in i && i.type === "divider") return "|";
      const key = i && "key" in i ? String(i.key) : "?";
      const kids = childrenOf(i);
      return kids.length > 0 ? `${key}>[${kids.map((c) => (c && "key" in c ? String(c.key) : "?")).join(",")}]` : key;
    });
    expect(shapeOf(items)).toEqual([
      "action:newFolder", "|", "action:upload", "action:uploadFolder", "|",
      "submenu:document>[blank:docx,template:document]",
      "submenu:spreadsheet>[blank:xlsx,template:spreadsheet]",
      "submenu:presentation>[blank:pptx,template:presentation]",
      "submenu:more>[blank:odg,blank:txt]",
    ]);
  });

  it("coarse: true draws no extra on any row, submenu titles included, and keeps aria-keyshortcuts", () => {
    const items = newMenuItems(tree(writable), opts({ coarse: true }));
    const found = allRows(items);
    expect(found.length).toBeGreaterThan(0);
    expect(found.every((i) => i && "extra" in i && i.extra === undefined)).toBe(true);
    expect(find(items, "action:newFolder")["aria-keyshortcuts"]).toBe("Alt+C F");
    expect(find(childrenOf(find(items, "submenu:document")), "blank:docx")["aria-keyshortcuts"]).toBe("Alt+C T");
  });

  it("coarse: false shows the hints as today, on the actions and the blank documents", () => {
    const items = newMenuItems(tree(writable), opts());
    const folderHint = find(items, "action:newFolder").extra;
    expect(folderHint && "props" in folderHint && (folderHint.props as Record<string, unknown>)["data-hint"]).toBe("Alt+C, F");
    const blankHint = find(childrenOf(find(items, "submenu:document")), "blank:docx").extra;
    expect(blankHint && "props" in blankHint && (blankHint.props as Record<string, unknown>)["data-hint"]).toBe("Alt+C, T");
  });

  it("phone: true ends each submenu row in a chevron that mirrors in RTL, and keeps its children for the slide", () => {
    const items = newMenuItems(tree(writable), opts({ phone: true }));
    for (const key of ["submenu:document", "submenu:spreadsheet", "submenu:presentation", "submenu:more"]) {
      const row = find(items, key);
      const chevron = row.extra;
      expect(chevron).toBeTruthy();
      expect(chevron && "type" in chevron && chevron.type).toBe(ChevronRight);
      expect(chevron && "props" in chevron && (chevron.props as Record<string, unknown>).className).toBe("icon-dir");
      expect(childrenOf(row).length).toBeGreaterThan(0);
    }
    // The plain actions keep their shape: the chevron belongs to the submenu rows alone.
    expect(find(items, "action:newFolder").extra && "type" in (find(items, "action:newFolder").extra as object)).toBe(true);
    expect((find(items, "action:newFolder").extra as { type: unknown }).type).not.toBe(ChevronRight);
  });
});
