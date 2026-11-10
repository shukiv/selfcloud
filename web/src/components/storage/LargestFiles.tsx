// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button, Tag } from "antd";
import { Download, RotateCcw, Trash2 } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { useAdminText } from "../../lib/manage/adminText";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import { coarsePointer, officeEdit, plainClick, type LinkClick } from "../../lib/files/editor";
import { get } from "../../lib/store";
import { contentPath } from "../../lib/files/api";
import { CHIPS, type ChipId } from "../../lib/storage/breakdown";
import type { LargestFile } from "../../lib/storage/api";
import Icon from "../files/Icon";
import Listing from "../files/Listing";

// LargestFilesCard is "Largest files" (decision 79): filter chips, then the one table of the files (decision 122) with their
// type, size, old versions and where they are, then the row actions (icons first, each with a tooltip, then More). "Show more"
// loads the next page. The chips ask the server for the kind, so the list stays the largest of that kind. Each row's bar shows
// its size against the largest row, and is decorative: the number beside it says the size. loaded says the first answer has
// come: until then the table is not drawn, so its empty message never shows before the list is read.
export default function LargestFilesCard({
  files,
  loaded,
  chip,
  onChip,
  hasMore,
  loading,
  onMore,
  onTrash,
  onRestore,
}: {
  files: LargestFile[];
  loaded: boolean;
  chip: ChipId;
  onChip: (id: ChipId) => void;
  hasMore: boolean;
  loading: boolean;
  onMore: () => void;
  onTrash: (id: string) => void;
  onRestore: (id: string) => void;
}) {
  const { t } = useT();
  const adminText = useAdminText();
  const lang = useLang();
  const top = files.reduce((m, f) => Math.max(m, f.size), 0);
  return (
    <div className="storage-card" style={{ padding: 0 }}>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: "12px 16px", padding: "16px 20px 12px" }}>
        {/* tabIndex -1 lets the page move the focus here when no row is left after an action (decision 79). */}
        <h2 id="largest-title" tabIndex={-1} style={{ margin: 0, fontSize: 16, fontWeight: 600, flex: "1 1 auto", outline: "none" }}>
          {t("storage.largest")}
        </h2>
        <div role="group" aria-label={t("storage.kindLabel")} style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
          {CHIPS.map((c) => {
            const on = c.id === chip;
            return (
              <button
                key={c.id}
                type="button"
                aria-pressed={on}
                className={on ? "storage-chip storage-chip-on" : "storage-chip"}
                onClick={() => onChip(c.id)}
              >
                {t(`storage.chip.${c.id}`)}
              </button>
            );
          })}
        </div>
      </div>

      {loaded && (
        <Listing
          label={t("storage.largest")}
          items={files}
          rowLabel={(f) => f.name}
          icon={(f) => <Icon kind={kindOf({ kind: "file", mime_type: f.mime_type })} node={{ kind: "file", mime_type: f.mime_type, name: f.name }} size={28} />}
          columns={[
            {
              key: "name",
              title: t("storage.name"),
              render: (f) => (
                <span>
                  <a href={`/n/${encodeURIComponent(f.id)}`} onClick={(e) => officeInNewTab(e, f.id, f.mime_type)}>
                    <bdi className="item-name">{f.name}</bdi>
                  </a>{" "}
                  {f.in_trash && <Tag>{t("storage.inTrash")}</Tag>}
                  <span style={{ display: "block", color: "var(--ant-color-text-secondary)", fontSize: 12 }}>
                    {f.parent_name ? t("storage.where", { folder: f.parent_name }) : t("storage.mine")}
                  </span>
                </span>
              ),
            },
            {
              key: "size",
              title: t("storage.size"),
              align: "end",
              render: (f) => (
                <span style={{ whiteSpace: "nowrap", fontVariantNumeric: "tabular-nums" }}>
                  {formatBytes(f.size, lang)}
                  <span
                    aria-hidden="true"
                    style={{ display: "block", height: 4, borderRadius: 2, marginTop: 6, background: "var(--ant-color-fill-secondary)" }}
                  >
                    <span
                      data-size-bar
                      style={{
                        display: "block",
                        height: 4,
                        borderRadius: 2,
                        width: `${top > 0 ? Math.round((f.size / top) * 100) : 0}%`,
                        background: "var(--ant-color-primary-text)",
                      }}
                    />
                  </span>
                </span>
              ),
            },
            {
              key: "versions",
              title: t("storage.oldVersions"),
              align: "end",
              secondary: true,
              render: (f) => (f.versions_bytes > 0 ? formatBytes(f.versions_bytes, lang) : "—"),
            },
            {
              key: "modified",
              title: t("storage.modified"),
              secondary: true,
              render: (f) => <time dateTime={f.modified_at}>{formatDate(f.modified_at, lang)}</time>,
            },
          ]}
          actions={(f) => [
            {
              key: "download",
              label: t("storage.act.downloadFor", { name: f.name }),
              icon: <Download size={18} aria-hidden="true" focusable="false" />,
              // A download is the file's own address with the attachment disposition: a link, so the browser saves it and a
              // middle click or Copy link works (round 2, M2). run serves the ⋮ menu's plain click.
              href: contentPath(f.id),
              run: () => window.location.assign(contentPath(f.id)),
            },
            f.in_trash
              ? {
                  key: "restore",
                  label: t("storage.act.restoreFor", { name: f.name }),
                  icon: <RotateCcw size={18} aria-hidden="true" focusable="false" />,
                  run: () => onRestore(f.id),
                }
              : {
                  key: "trash",
                  label: t("storage.act.trashFor", { name: f.name }),
                  icon: <Trash2 size={18} aria-hidden="true" focusable="false" />,
                  run: () => onTrash(f.id),
                },
          ]}
          menu={(f) => [
            {
              key: "open",
              label: t("files.act.open"),
              href: `/n/${encodeURIComponent(f.id)}`,
              // A plain click or a key opens the file here, or in the editor's own tab for an office file (the rules below).
              onSelect: () => {
                if (!openFileTab(f.id, f.mime_type)) window.location.assign(`/n/${encodeURIComponent(f.id)}`);
              },
            },
          ]}
          // A row opens its file as the menu's Open does (an office file in its editor's tab, another in this tab).
          onOpen={(f) => {
            if (!openFileTab(f.id, f.mime_type)) window.open(`/n/${encodeURIComponent(f.id)}`, "_self");
          }}
          empty={{ title: t("storage.largestEmpty"), description: adminText ? t("manage.empty.largest") : undefined }}
        />
      )}

      {hasMore && (
        <div style={{ display: "flex", justifyContent: "center", padding: 12 }}>
          <Button disabled={loading} onClick={onMore}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}

// openFileTab opens an office file's editor in a new tab, when the rules of the editor's open say so (decision 102, round 2 R6,
// and the review: office editing on, a desktop pointer, an office file). The address is /n/{id}?tab=1: the /n/ page reaches the
// editor for the person's role, and the tab flag lets the editor's Close end the tab. It reports whether it opened one.
function openFileTab(id: string, mime: string | undefined): boolean {
  if (!get(officeEdit) || coarsePointer() || kindOf({ kind: "file", mime_type: mime }) !== "office") return false;
  window.open(`/n/${encodeURIComponent(id)}?tab=1`, "_blank", "noopener");
  return true;
}

// officeInNewTab is a link's click: a plain click on an office file opens its editor in a new tab, and any other click keeps
// the link's own address, for the browser's own choice (a modified click, a file that is not an office document, office
// editing off, and a coarse pointer, which opens the same tab, as the editor's open does on a phone).
function officeInNewTab(e: LinkClick, id: string, mime: string | undefined): void {
  if (!plainClick(e)) return;
  if (openFileTab(id, mime)) e.preventDefault();
}
