// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useId, useRef, useState, type CSSProperties, type KeyboardEvent } from "react";
import { Button, ConfigProvider, Dropdown, Tooltip, theme, type MenuProps } from "antd";
import { useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";
import { useMenuHeaderInks } from "../menuHeader";
import { useT } from "../../lib/i18n";
import { newEntries, type DocKind, type NewKey, type NewPlace } from "../../lib/files/newPlace";
import { menuIsDark, newMenuItems, newMenuTree, type NewBlank, type NewMenuNode } from "../../lib/files/newMenu";
import { newURL } from "../../lib/files/newRoute";
import { IDLE, chordBlocked, readChord, type ChordState, type NewChordAction } from "../../lib/files/newShortcut";
import type { UploadWhere } from "../../lib/files/ops";
import type { Sidebar } from "../../theme/tokens";

// The sidebar's New button (decision 66): a raised, rounded button at the top of the sidebar, with the New menu (newMenu.ts, the one
// list the files page's New reads too). Its plus and its word sit in the rows' icon column and name column (--sc-icon-col, set by the
// shell). The shell does the picking and the dialog: onPick and onFolder are called from the menu's click, so the file picker opens
// inside the click. The chord (Alt+C, then a letter) is read here too, with the menu closed: it runs the action and opens nothing.

export interface SidebarNewProps {
  // place is where the New actions act, and what it takes. null: the folder shown is not read yet, so the button waits.
  place: NewPlace | null;
  // failure is the error text when the folder shown could not be read (null otherwise): the reason a disabled button gives in
  // place of "Loading the folder" (wf1, review item 2).
  failure?: string | null;
  collapsed: boolean;
  coarse: boolean;
  sidebar: Sidebar;
  onPick: (kind: "files" | "folders", where: UploadWhere) => void;
  onFolder: (where: UploadWhere) => void;
  // documents are the blank Office documents the menu offers here (documentKinds: none unless office editing is on, decision 91).
  documents: DocKind[];
  // onDocument makes a blank file of kind: an Office kind, or Drawing (odg) or Text file (txt) from More.
  onDocument: (kind: NewBlank) => void;
}

const ic = { size: 18, strokeWidth: 1.75, "aria-hidden": true } as const;

// The raised face: the sidebar's white with its blue word, and a shadow that reads on the blue in light and in dark.
const RAISED = "0 1px 2px rgba(0, 0, 0, 0.3), 0 3px 8px rgba(0, 0, 0, 0.28)";

// The menu's width (design-nt2 section 3): 400 px at most, 320 px at least, and never wider than the screen less a 16 px gutter each side.
const MENU_STYLE = { width: "min(400px, calc(100vw - 32px))", minWidth: 320, boxSizing: "border-box" } as const;

// useNewChord reads the chord on the window, so it works with the menu closed (Alt+C, then a letter; newShortcut.ts). enabled is read
// on each key, and run is the latest one. A key another listener has already taken (its default cancelled) is left alone, so when the
// sidebar's New and the toolbar's New are both mounted, one of them acts on a key, and not both.
export function useNewChord(enabled: () => boolean, run: (action: NewChordAction) => void): void {
  const chord = useRef<ChordState>(IDLE);
  const latest = useRef({ enabled, run });
  latest.current = { enabled, run };
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.defaultPrevented || !latest.current.enabled()) return;
      const step = readChord(chord.current, e, Date.now(), chordBlocked(e.target));
      chord.current = step.state;
      if (step.consume) e.preventDefault();
      if (step.action) latest.current.run(step.action);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}

export default function SidebarNew({ place, failure, collapsed, coarse, sidebar, onPick, onFolder, documents, onDocument }: SidebarNewProps) {
  const { t } = useT();
  const { token } = theme.useToken();
  const reasonId = useId();
  // While the place is read, or could not be (failure), the button says why it waits: the hover tooltip shows the reason, and
  // the button's accessible description is the same text, always present while disabled, so a keyboard or screen-reader user
  // gets it too (wf1, review item 1). A disabled button fires no hover events, so the tooltip hangs on the span around it.
  const waitId = useId();
  const inks = useMenuHeaderInks();
  const navigate = useNavigate();
  // The menu's words come from the catalogue; the labels of the rows are the same ones the files page's New reads.
  const text = {
    newFolder: t("files.act.newFolder"),
    upload: t("files.act.upload"),
    uploadFolder: t("files.act.uploadFolder"),
    document: t("files.act.document"),
    spreadsheet: t("files.act.spreadsheet"),
    presentation: t("files.act.presentation"),
    more: t("files.new.more"),
    drawing: t("files.new.drawing"),
    textFile: t("files.new.textFile"),
    blank: t("files.new.blank"),
    fromTemplate: t("files.new.fromTemplate"),
    shortcut: t("files.new.shortcut"),
  };
  // The dark menu is the one whose ground is dark: read from the antd token, which the palette sets (no provider needed here).
  const dark = menuIsDark(String(token.colorBgElevated));
  const writable = place !== null && !place.readOnly;
  const tree = place ? newMenuTree({ entries: newEntries(place), blanks: documents, hints: writable }) : [];
  const body: NonNullable<MenuProps["items"]> = newMenuItems(tree, { text, dark, coarse, onRun: (node) => run(node) });
  // In a folder the person cannot add to, the reason is the title of the menu's group (decision 95), not a disabled item, and the menu
  // is named by it. Its ink is set from menuHeaderInks, since antd paints a group title in its description colour.
  const items: MenuProps["items"] = place?.readOnly
    ? [{ type: "group", key: "reason", label: <span id={reasonId} style={{ color: inks.email, fontSize: token.fontSizeSM }}>{t("files.new.readOnly")}</span>, children: body }]
    : body;
  // runChordAction is the chord's letter as an action in this place: the same calls the menu's rows make.
  function runChordAction(action: NewChordAction) {
    if (!place) return;
    const enabled = (key: NewKey) => newEntries(place).some((e) => e.key === key && e.enabled);
    if (action === "newFolder" && enabled("newFolder")) onFolder(place.where);
    if (action === "upload" && enabled("upload")) onPick("files", place.where);
    if (action === "uploadFolder" && enabled("uploadFolder")) onPick("folders", place.where);
    if ((action === "docx" || action === "xlsx" || action === "pptx") && documents.includes(action)) onDocument(action);
  }
  // run does what a menu row or a chord letter asks for in the place. A template goes to the gallery at the place's folder and
  // directory, with the kind (newURL); a blank file goes to onDocument (decision 102).
  function run(node: NewMenuNode) {
    if (!place) return;
    if (node.type === "action") {
      if (node.key === "newFolder") onFolder(place.where);
      if (node.key === "upload") onPick("files", place.where);
      if (node.key === "uploadFolder") onPick("folders", place.where);
      return;
    }
    if (node.type === "blank") onDocument(node.kind);
    if (node.type === "template") navigate(newURL(place.where.folder, place.where.dirID, node.kind));
  }
  const menu = { items, onClick: () => undefined, style: MENU_STYLE, "aria-labelledby": place?.readOnly ? reasonId : undefined };
  // A place that is still being read shows the button, disabled.
  const disabled = place === null;
  const reason = disabled ? failure || t("files.loading") : null;
  // The menu is controlled so Escape can close it here. In the phone drawer the drawer's panel takes Escape from the menu and closes
  // the drawer, and the menu stayed open (decision 95, round 3). So while the menu is open, Escape is taken by the wrapper around the
  // dropdown, which sits between the menu and the drawer: it closes the menu and returns focus to the button.
  const [open, setOpen] = useState(false);
  const newButton = useRef<HTMLButtonElement>(null);
  const takeEscape = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!open || e.key !== "Escape") return;
    e.stopPropagation();
    setOpen(false);
    newButton.current?.focus();
  };
  // The chord (Alt+C, then a letter) runs the action with the menu closed. It is on while the place takes new files.
  useNewChord(() => writable, runChordAction);
  if (collapsed) {
    // The folded rail keeps a round button with the same menu; its name is in the tooltip and is its accessible name.
    // Its centre is the top bar's (28 px): a 44 px circle starts 6 px down. The wrapper centres it in the rail.
    const round: CSSProperties = { inlineSize: 44, blockSize: 44, minInlineSize: 44, boxShadow: RAISED, background: sidebar.text, color: sidebar.bg, border: "none" };
    return (
      <div onKeyDown={takeEscape} style={{ display: "flex", justifyContent: "center", marginBlockStart: 6 }}>
        {reason && <span id={waitId} className="sr-only" aria-hidden="true">{reason}</span>}
        <Tooltip title={reason ?? t("files.act.new")} placement="right">
          <span data-testid="new-tip" style={{ display: "inline-flex" }}>
            <Dropdown trigger={["click"]} open={open} onOpenChange={setOpen} disabled={disabled} menu={menu}>
              <Button ref={newButton} data-testid="new-button" aria-label={t("files.act.new")} aria-haspopup="menu" aria-describedby={reason ? waitId : undefined} shape="circle" disabled={disabled} icon={<Plus {...ic} />} style={round} />
            </Dropdown>
          </span>
        </Tooltip>
      </div>
    );
  }
  const face: CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    gap: 10,
    // As wide as its text (decision 69): the content, the start column and an end padding of 24 px, capped at the sidebar's width.
    inlineSize: "auto",
    maxInlineSize: "100%",
    minInlineSize: 0,
    minBlockSize: 56,
    // The plus starts in the icon column (the shell sets --sc-icon-col from the sidebar's content edge), and its word in the name
    // column (18 px icon, 10 px gap), as the rows below it.
    paddingInlineStart: "var(--sc-icon-col, 28px)",
    paddingInlineEnd: 24,
    borderRadius: 16,
    border: "none",
    background: sidebar.text,
    color: sidebar.bg,
    boxShadow: RAISED,
    fontSize: 14,
    fontWeight: 600,
    lineHeight: "20px",
  };
  return (
    // The top edge is the search box's top edge in the top bar: (56 - 36) / 2 = 10 px, or (56 - 44) / 2 = 6 px on a finger.
    <div onKeyDown={takeEscape} style={{ display: "flex", justifyContent: "flex-start", marginBlockStart: coarse ? 6 : 10, marginBlockEnd: 16, minInlineSize: 0 }}>
      <ConfigProvider theme={{ components: { Menu: { itemHeight: coarse ? 44 : 40 } } }}>
        {reason && <span id={waitId} className="sr-only" aria-hidden="true">{reason}</span>}
        <Tooltip title={reason ?? undefined}>
          <span data-testid="new-tip" style={{ display: "inline-flex", maxInlineSize: "100%", minInlineSize: 0 }}>
            <Dropdown trigger={["click"]} open={open} onOpenChange={setOpen} disabled={disabled} menu={menu}>
              <Button ref={newButton} data-testid="new-button" aria-haspopup="menu" aria-describedby={reason ? waitId : undefined} disabled={disabled} style={face}>
                <Plus {...ic} />
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t("files.act.new")}</span>
              </Button>
            </Dropdown>
          </span>
        </Tooltip>
      </ConfigProvider>
    </div>
  );
}
