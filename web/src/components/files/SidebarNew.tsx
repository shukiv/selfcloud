// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties, ReactNode } from "react";
import { Button, ConfigProvider, Dropdown, Tooltip, type MenuProps } from "antd";
import { FolderPlus, FolderUp, Plus, Upload } from "lucide-react";
import { useT } from "../../lib/i18n";
import { newEntries, type NewKey, type NewPlace } from "../../lib/files/newPlace";
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
}

const ic = { size: 18, strokeWidth: 1.75, "aria-hidden": true } as const;

// The raised face: the sidebar's white with its blue word, and a shadow that reads on the blue in light and in dark.
const RAISED = "0 1px 2px rgba(0, 0, 0, 0.3), 0 3px 8px rgba(0, 0, 0, 0.28)";

export default function SidebarNew({ place, collapsed, coarse, sidebar, onPick, onFolder }: SidebarNewProps) {
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
  const items: MenuProps["items"] = [
    ...(place?.readOnly ? [{ key: "reason", disabled: true, label: t("files.new.readOnly") }, { type: "divider" as const, key: "rule" }] : []),
    ...(place ? newEntries(place) : [])
      .filter((e) => e.visible)
      .map((e) => ({ key: e.key, label: text[e.key], icon: icons[e.key], disabled: !e.enabled })),
  ];
  const choose = (key: string) => {
    if (!place) return;
    if (key === "newFolder") onFolder(place.where);
    if (key === "upload") onPick("files", place.where);
    if (key === "uploadFolder") onPick("folders", place.where);
  };
  const menu = { items, onClick: ({ key }: { key: string }) => choose(key) };
  // A place that is still being read shows the button, disabled.
  const disabled = place === null;
  if (collapsed) {
    // The folded rail keeps a round button with the same menu; its name is in the tooltip and is its accessible name.
    const round: CSSProperties = { inlineSize: 44, blockSize: 44, minInlineSize: 44, marginInline: "auto", boxShadow: RAISED, background: sidebar.text, color: sidebar.bg, border: "none" };
    return (
      <Tooltip title={t("files.act.new")} placement="right">
        <Dropdown trigger={["click"]} disabled={disabled} menu={menu}>
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
    inlineSize: "100%",
    minBlockSize: 56,
    // The plus starts in the icon column (the shell sets --sc-icon-col from the sidebar's content edge), and its word in the name
    // column (18 px icon, 10 px gap), as the rows below it.
    paddingInlineStart: "var(--sc-icon-col, 28px)",
    paddingInlineEnd: 16,
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
    <div style={{ marginBlockEnd: 16 }}>
      <ConfigProvider theme={{ components: { Menu: { itemHeight: coarse ? 44 : 36 } } }}>
        <Dropdown trigger={["click"]} disabled={disabled} menu={menu}>
          <Button data-testid="new-button" aria-haspopup="menu" disabled={disabled} style={face}>
            <Plus {...ic} />
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t("files.act.new")}</span>
          </Button>
        </Dropdown>
      </ConfigProvider>
    </div>
  );
}
