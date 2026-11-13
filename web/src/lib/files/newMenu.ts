// SPDX-License-Identifier: AGPL-3.0-or-later

// The New menu, one list for the sidebar's New and the files page's New (decision 108). newMenuTree
// says what the menu holds, as data: the groups in order, the submenus (Blank, From a template), the direct items (Drawing, Text
// file under More), and the chord letters. newMenuItems turns that tree into the antd items both callers render, with the labels,
// the icons, the row heights and the hints, so the two menus cannot drift apart.
import { createElement, type CSSProperties } from "react";
import type { MenuProps } from "antd";
import { AlignLeft, FilePlus, FileSpreadsheet, FileText, FolderPlus, FolderUp, LayoutTemplate, MoreHorizontal, Palette, Presentation, Upload } from "lucide-react";
import { CHORD_LETTER, type NewChordAction } from "./newShortcut";
import { contrast } from "../brand";
import { DOC_KINDS, type BlankKind, type DocKind, type NewEntry, type NewKey } from "./newPlace";
import type { NewGalleryKind } from "./newRoute";

// NewBlank is a blank file the menu makes: an Office kind (through its submenu, or direct), or the two OpenDocument and text kinds
// that are direct items under More (they have no template).
export type NewBlank = BlankKind;

// NewSubmenu is a submenu of the menu: one per Office kind, and More (Drawing and Text file).
export type NewSubmenu = "document" | "spreadsheet" | "presentation" | "more";

// NewMenuNode is one entry of the menu, as data.
export type NewMenuNode =
  | { type: "action"; key: NewKey; enabled: boolean; chord?: NewChordAction }
  | { type: "divider" }
  | { type: "submenu"; key: NewSubmenu; children: NewMenuNode[] }
  | { type: "blank"; kind: NewBlank; chord?: NewChordAction }
  | { type: "template"; kind: NewGalleryKind };

// NewMenuInput is what a place offers: its three actions (newEntries), the blank kinds it offers (documentKinds), and whether it
// shows the chord hints (a writable place only: a viewer sees none).
export interface NewMenuInput {
  entries: NewEntry[];
  blanks: BlankKind[];
  hints: boolean;
}

// The Office kinds, each with its submenu and the gallery's kind (decision 105 names the gallery by these).
const KIND_SUBMENU: Record<DocKind, { sub: NewSubmenu; gallery: NewGalleryKind; chord: NewChordAction }> = {
  docx: { sub: "document", gallery: "document", chord: "docx" },
  xlsx: { sub: "spreadsheet", gallery: "spreadsheet", chord: "xlsx" },
  pptx: { sub: "presentation", gallery: "presentation", chord: "pptx" },
};

// The direct items under More: Drawing and Text file. They have no template and no chord letter.
const MORE_KINDS: NewBlank[] = ["odg", "txt"];

// newMenuTree is the New menu for a place, as data. The groups are: New folder | Upload files, Upload folder | the Office kinds
// (each a submenu of Blank and From a template) and More (Drawing, Text file). A group with nothing in it is not there, and a
// divider sits between the groups that are.
export function newMenuTree(input: NewMenuInput): NewMenuNode[] {
  const actions: NewMenuNode[] = input.entries
    .filter((e) => e.visible)
    .map((e) => {
      const node: NewMenuNode = { type: "action", key: e.key, enabled: e.enabled };
      if (input.hints && e.enabled) return { ...node, chord: chordOfAction(e.key) };
      return node;
    });
  const folder = actions.filter((a) => a.type === "action" && a.key === "newFolder");
  const uploads = actions.filter((a) => a.type === "action" && a.key !== "newFolder");
  const kinds: NewMenuNode[] = DOC_KINDS.filter((k) => input.blanks.includes(k)).map((k) => ({
    type: "submenu" as const,
    key: KIND_SUBMENU[k].sub,
    children: [
      { type: "blank" as const, kind: k, ...(input.hints ? { chord: KIND_SUBMENU[k].chord } : {}) },
      { type: "template" as const, kind: KIND_SUBMENU[k].gallery },
    ],
  }));
  // More holds the blank kinds the place offers from MORE_KINDS (Drawing, then Text file): with office editing off, Text file alone.
  const moreKinds = MORE_KINDS.filter((k) => input.blanks.includes(k));
  const more: NewMenuNode[] = moreKinds.length > 0 ? [{ type: "submenu", key: "more", children: moreKinds.map((kind) => ({ type: "blank" as const, kind })) }] : [];
  const groups = [folder, uploads, [...kinds, ...more]].filter((g) => g.length > 0);
  return groups.flatMap((g, i) => (i === 0 ? g : [{ type: "divider" as const }, ...g]));
}

function chordOfAction(key: NewKey): NewChordAction {
  return key === "newFolder" ? "newFolder" : key === "upload" ? "upload" : "uploadFolder";
}

// KIND_INK is the colour of each kind's icon, in light and in dark. Each is at least 4.5:1 on the menu's ground in its theme
// (the test reads the ground from the palette).
export type InkKind = "document" | "spreadsheet" | "presentation" | "drawing" | "text";
export const KIND_INK: { light: Record<InkKind, string>; dark: Record<InkKind, string> } = {
  light: { document: "#1d4ed8", spreadsheet: "#15803d", presentation: "#b45309", drawing: "#7e22ce", text: "#475569" },
  dark: { document: "#93c5fd", spreadsheet: "#86efac", presentation: "#fdba74", drawing: "#d8b4fe", text: "#cbd5e1" },
};

// menuIsDark says whether a menu on ground (its background colour) is a dark menu: the ground is darker against black than against white.
export function menuIsDark(ground: string): boolean {
  return contrast(ground, "#000000") > contrast(ground, "#ffffff");
}

// NewMenuText is the words of the menu, read from the catalogue by the caller.
export interface NewMenuText {
  newFolder: string;
  upload: string;
  uploadFolder: string;
  document: string;
  spreadsheet: string;
  presentation: string;
  more: string;
  drawing: string;
  textFile: string;
  blank: string;
  fromTemplate: string;
  shortcut: string;
}

export interface NewMenuOptions {
  text: NewMenuText;
  dark: boolean;
  coarse: boolean;
  // onRun is called with the node chosen: an action, a blank kind, or a template kind.
  onRun: (node: NewMenuNode) => void;
}

const ICON = { size: 20, strokeWidth: 1.75, "aria-hidden": true } as const;

// nodeKey is the antd key of a node: unique in its menu, so the items keep their identity across renders.
function nodeKey(node: NewMenuNode, index: number): string {
  switch (node.type) {
    case "action": return `action:${node.key}`;
    case "blank": return `blank:${node.kind}`;
    case "template": return `template:${node.kind}`;
    case "submenu": return `submenu:${node.key}`;
    case "divider": return `divider:${index}`;
  }
}

// MENU_ROW_HEIGHT is the New menu's row height in pixels: 40, or 44 on a coarse pointer. The row style below sets it on each
// item (the file menu sizes its own rows), and a submenu title fills its wrapper, so the title takes that row's height.
export const MENU_ROW_HEIGHT = { fine: 40, coarse: 44 } as const;

// newMenuItems builds the antd items of the tree. A row is 40 px, or 44 px on a coarse pointer; the chord letter sits at the row's
// end (in RTL, the start), and the row names its keys to a screen reader through aria-keyshortcuts.
export function newMenuItems(tree: NewMenuNode[], o: NewMenuOptions): NonNullable<MenuProps["items"]> {
  // The height is the row's whole box (border-box), so the antd item's padding is inside the 40 or 44 px. antd centres the row's
  // icon and label in it (align-items: center), so the row needs no line-height of its own.
  const rowPx = o.coarse ? MENU_ROW_HEIGHT.coarse : MENU_ROW_HEIGHT.fine;
  const rowStyle: CSSProperties = { height: rowPx, boxSizing: "border-box" };
  const ink = o.dark ? KIND_INK.dark : KIND_INK.light;
  const hint = (chord: NewChordAction | undefined) =>
    chord === undefined ? undefined : createElement("span", { "aria-hidden": "true", className: "menu-hint", "data-hint": `Alt+C, ${CHORD_LETTER[chord]}`, title: o.text.shortcut });
  return tree.map((node, index): NonNullable<MenuProps["items"]>[number] => {
    const key = nodeKey(node, index);
    switch (node.type) {
      case "divider":
        return { type: "divider", key };
      case "action": {
        const icon = actionIcon(node.key);
        return {
          key,
          label: actionLabel(node.key, o.text),
          icon: createElement(icon, { ...ICON }),
          disabled: !node.enabled,
          extra: hint(node.chord),
          ...keyShortcuts(node.chord),
          style: rowStyle,
          onClick: () => o.onRun(node),
        };
      }
      case "blank": {
        // Under an Office kind, Blank is the kind's first row. Under More, Drawing and Text file are the rows themselves.
        const label = node.kind === "odg" ? o.text.drawing : node.kind === "txt" ? o.text.textFile : o.text.blank;
        const color = node.kind === "odg" ? ink.drawing : node.kind === "txt" ? ink.text : blankInk(node.kind, ink);
        const icon = node.kind === "odg" ? Palette : node.kind === "txt" ? AlignLeft : FilePlus;
        return {
          key,
          label,
          icon: createElement(icon, { ...ICON, color }),
          extra: hint(node.chord),
          ...keyShortcuts(node.chord),
          style: rowStyle,
          onClick: () => o.onRun(node),
        };
      }
      case "template":
        return {
          key,
          label: o.text.fromTemplate,
          icon: createElement(LayoutTemplate, { ...ICON }),
          style: rowStyle,
          onClick: () => o.onRun(node),
        };
      case "submenu": {
        const isMore = node.key === "more";
        const label = isMore ? o.text.more : kindLabel(node.key, o.text);
        const icon = isMore ? MoreHorizontal : kindIcon(node.key);
        const color = isMore ? undefined : blankInk(node.key, ink);
        return {
          key,
          label,
          icon: createElement(icon, { ...ICON, ...(color ? { color } : {}) }),
          style: rowStyle,
          children: newMenuItems(node.children, o),
        };
      }
    }
  });
}

// keyShortcuts is the row's aria-keyshortcuts, when it has a chord letter (a spread, so antd's item type takes it).
function keyShortcuts(chord: NewChordAction | undefined): Record<string, string> {
  return chord === undefined ? {} : { "aria-keyshortcuts": `Alt+C ${CHORD_LETTER[chord]}` };
}

function actionLabel(key: NewKey, text: NewMenuText): string {
  return key === "newFolder" ? text.newFolder : key === "upload" ? text.upload : text.uploadFolder;
}

function actionIcon(key: NewKey) {
  return key === "newFolder" ? FolderPlus : key === "upload" ? Upload : FolderUp;
}

function kindLabel(key: NewSubmenu, text: NewMenuText): string {
  return key === "document" ? text.document : key === "spreadsheet" ? text.spreadsheet : text.presentation;
}

function kindIcon(key: NewSubmenu) {
  return key === "document" ? FileText : key === "spreadsheet" ? FileSpreadsheet : Presentation;
}

// blankInk is the icon colour of an Office kind or submenu, by its name.
function blankInk(key: NewBlank | NewSubmenu, ink: Record<InkKind, string>): string {
  if (key === "docx" || key === "document") return ink.document;
  if (key === "xlsx" || key === "spreadsheet") return ink.spreadsheet;
  return ink.presentation;
}
