// SPDX-License-Identifier: AGPL-3.0-or-later

import { useId, type CSSProperties, type ReactNode } from "react";
import { Button, ConfigProvider, Dropdown, Tooltip, theme, type MenuProps } from "antd";
import { MenuHeader, headerPopup } from "../menuHeader";
import { FileSpreadsheet, FileText, FolderPlus, FolderUp, Plus, Presentation, Upload } from "lucide-react";
import { useT } from "../../lib/i18n";
import { DOC_KINDS, newEntries, type DocKind, type NewKey, type NewPlace } from "../../lib/files/newPlace";
import type { UploadWhere } from "../../lib/files/ops";
import type { Sidebar } from "../../theme/tokens";

// The sidebar's New button (decision 66): a raised, rounded button at the top of the sidebar, with the toolbar's New menu. Its
// plus and its word sit in the rows' icon column and name column (--sc-icon-col, set by the shell). The shell does the picking
// and the dialog: onPick and onFolder are called from the menu's click, so the file picker opens inside the click.

export interface SidebarNewProps {
  // place is where the New actions act, and what it takes. null: the folder shown is not read yet, so the button waits.
  place: NewPlace | null;
  collapsed: boolean;
  coarse: boolean;
  sidebar: Sidebar;
  onPick: (kind: "files" | "folders", where: UploadWhere) => void;
  onFolder: (where: UploadWhere) => void;
  // documents are the blank Office documents the menu offers here (documentKinds: none unless office editing is on, decision 91).
  documents: DocKind[];
  onDocument: (kind: DocKind) => void;
}

const ic = { size: 18, strokeWidth: 1.75, "aria-hidden": true } as const;

// The raised face: the sidebar's white with its blue word, and a shadow that reads on the blue in light and in dark.
const RAISED = "0 1px 2px rgba(0, 0, 0, 0.3), 0 3px 8px rgba(0, 0, 0, 0.28)";

export default function SidebarNew({ place, collapsed, coarse, sidebar, onPick, onFolder, documents, onDocument }: SidebarNewProps) {
  const { t } = useT();
  const text: Record<NewKey, string> = {
    newFolder: t("files.act.newFolder"),
    upload: t("files.act.upload"),
    uploadFolder: t("files.act.uploadFolder"),
  };
  const icons: Record<NewKey, ReactNode> = {
    newFolder: <FolderPlus {...ic} />,
    upload: <Upload {...ic} />,
    uploadFolder: <FolderUp {...ic} />,
  };
  const { token } = theme.useToken();
  const reasonId = useId();
  // The blank documents (decision 91) come after the uploads, behind a divider, each with its icon and its word.
  const docText: Record<DocKind, string> = { docx: t("files.act.document"), xlsx: t("files.act.spreadsheet"), pptx: t("files.act.presentation") };
  const docIcons: Record<DocKind, ReactNode> = { docx: <FileText {...ic} />, xlsx: <FileSpreadsheet {...ic} />, pptx: <Presentation {...ic} /> };
  const docItems: NonNullable<MenuProps["items"]> = DOC_KINDS.filter((k) => documents.includes(k)).map((k) => ({
    key: `doc-${k}`,
    label: docText[k],
    icon: docIcons[k],
  }));
  // In a folder the person cannot add to, the reason is the menu's header (decision 95), not a disabled item: its text names the menu.
  const header = place?.readOnly ? (
    <MenuHeader>
      <span id={reasonId} style={{ display: "block", color: token.colorText, fontSize: token.fontSizeSM }}>
        {t("files.new.readOnly")}
      </span>
    </MenuHeader>
  ) : null;
  const items: MenuProps["items"] = [
    ...(place ? newEntries(place) : [])
      .filter((e) => e.visible)
      .map((e) => ({ key: e.key, label: text[e.key], icon: icons[e.key], disabled: !e.enabled })),
    ...(docItems.length > 0 ? [{ type: "divider" as const, key: "documents" }, ...docItems] : []),
  ];
  const choose = (key: string) => {
    if (!place) return;
    if (key === "newFolder") onFolder(place.where);
    if (key === "upload") onPick("files", place.where);
    if (key === "uploadFolder") onPick("folders", place.where);
    if (key.startsWith("doc-")) onDocument(key.slice(4) as DocKind);
  };
  const menu = { items, onClick: ({ key }: { key: string }) => choose(key), "aria-labelledby": header ? reasonId : undefined };
  const popup = headerPopup(header);
  // A place that is still being read shows the button, disabled.
  const disabled = place === null;
  if (collapsed) {
    // The folded rail keeps a round button with the same menu; its name is in the tooltip and is its accessible name.
    // Its centre is the top bar's (28 px): a 44 px circle starts 6 px down.
    const round: CSSProperties = { inlineSize: 44, blockSize: 44, minInlineSize: 44, marginBlockStart: 6, marginInline: "auto", boxShadow: RAISED, background: sidebar.text, color: sidebar.bg, border: "none" };
    return (
      <Tooltip title={t("files.act.new")} placement="right">
        <Dropdown trigger={["click"]} disabled={disabled} menu={menu} popupRender={popup}>
          <Button data-testid="new-button" aria-label={t("files.act.new")} aria-haspopup="menu" shape="circle" disabled={disabled} icon={<Plus {...ic} />} style={round} />
        </Dropdown>
      </Tooltip>
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
    <div style={{ display: "flex", justifyContent: "flex-start", marginBlockStart: coarse ? 6 : 10, marginBlockEnd: 16, minInlineSize: 0 }}>
      <ConfigProvider theme={{ components: { Menu: { itemHeight: coarse ? 44 : 36 } } }}>
        <Dropdown trigger={["click"]} disabled={disabled} menu={menu} popupRender={popup}>
          <Button data-testid="new-button" aria-haspopup="menu" disabled={disabled} style={face}>
            <Plus {...ic} />
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t("files.act.new")}</span>
          </Button>
        </Dropdown>
      </ConfigProvider>
    </div>
  );
}
