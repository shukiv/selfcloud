// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button, Dropdown, Tag, Tooltip, type MenuProps } from "antd";
import { Download, MoreHorizontal, RotateCcw, Trash2 } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import { plainClick, type LinkClick } from "../../lib/files/editor";
import { contentPath } from "../../lib/files/api";
import { CHIPS, type ChipId } from "../../lib/storage/breakdown";
import type { LargestFile } from "../../lib/storage/api";
import Icon from "../files/Icon";

// LargestFilesCard is "Largest files" (decision 79): filter chips, then a table of the files with their type, size, old
// versions and where they are, then the row actions (icons first, each with a tooltip, then More). "Show more" loads the
// next page. The chips ask the server for the kind, so the list stays the largest of that kind. Each row's bar shows its
// size against the largest row, and is decorative: the number beside it says the size.
export default function LargestFilesCard({
  files,
  chip,
  onChip,
  hasMore,
  loading,
  onMore,
  onTrash,
  onRestore,
}: {
  files: LargestFile[];
  chip: ChipId;
  onChip: (id: ChipId) => void;
  hasMore: boolean;
  loading: boolean;
  onMore: () => void;
  onTrash: (id: string) => void;
  onRestore: (id: string) => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const top = files.reduce((m, f) => Math.max(m, f.size), 0);
  return (
    <section aria-labelledby="largest-title" className="storage-card" style={{ padding: 0 }}>
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

      {/* The table keeps its columns at phone width; the old versions and the date go at narrow widths (CSS: .storage-wide). */}
      <div style={{ overflowX: "hidden" }}>
        <table className="storage-table" style={{ width: "100%", borderCollapse: "collapse", fontSize: 14 }}>
          <thead>
            <tr>
              <th scope="col" style={{ textAlign: "start", paddingBlock: 8, paddingInlineStart: 20, paddingInlineEnd: 8 }}>{t("storage.name")}</th>
              <th scope="col" style={{ textAlign: "end", padding: 8 }}>{t("storage.size")}</th>
              <th scope="col" className="storage-wide" style={{ textAlign: "end", padding: 8 }}>{t("storage.oldVersions")}</th>
              <th scope="col" className="storage-wide" style={{ textAlign: "start", padding: 8 }}>{t("storage.modified")}</th>
              <th scope="col" style={{ paddingBlock: 8, paddingInlineStart: 8, paddingInlineEnd: 12 }}>
                <span className="sr-only">{t("storage.actions")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {files.map((f, i) => (
              <tr key={f.id} data-row-index={i} style={{ borderBlockStart: "1px solid var(--ant-color-border-secondary)" }}>
                <th scope="row" style={{ textAlign: "start", paddingBlock: 10, paddingInlineStart: 20, paddingInlineEnd: 8, fontWeight: 600, overflowWrap: "anywhere" }}>
                  <span style={{ display: "flex", alignItems: "center", gap: 12, minWidth: 0 }}>
                    <Icon kind={kindOf({ kind: "file", mime_type: f.mime_type })} node={{ kind: "file", mime_type: f.mime_type, name: f.name }} size={28} />
                    <span style={{ minWidth: 0 }}>
                      <a href={`/n/${encodeURIComponent(f.id)}`} onClick={(e) => officeInNewTab(e, f.id, f.mime_type)}>
                        <bdi>{f.name}</bdi>
                      </a>{" "}
                      {f.in_trash && <Tag>{t("storage.inTrash")}</Tag>}
                      <span style={{ display: "block", color: "var(--ant-color-text-secondary)", fontSize: 12, fontWeight: 400 }}>
                        {f.parent_name ? t("storage.where", { folder: f.parent_name }) : t("storage.mine")}
                      </span>
                    </span>
                  </span>
                </th>
                <td style={{ textAlign: "end", padding: 8, fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>
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
                </td>
                <td className="storage-wide" style={{ textAlign: "end", padding: 8, fontVariantNumeric: "tabular-nums" }}>
                  {f.versions_bytes > 0 ? formatBytes(f.versions_bytes, lang) : "—"}
                </td>
                <td className="storage-wide" style={{ textAlign: "start", padding: 8 }}>
                  <time dateTime={f.modified_at}>{formatDate(f.modified_at, lang)}</time>
                </td>
                <td style={{ paddingBlock: 4, paddingInlineStart: 8, paddingInlineEnd: 12, whiteSpace: "nowrap" }}>
                  <RowActions file={f} onTrash={onTrash} onRestore={onRestore} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {hasMore && (
        <div style={{ display: "flex", justifyContent: "center", padding: 12 }}>
          <Button disabled={loading} onClick={onMore}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}
    </section>
  );
}

// officeInNewTab opens an office file's address in a new tab, from the click (decision 102, round 2 R6). The address reaches the
// editor there, for the person's role, through the /n/ route. A file that is not an office document keeps its link, and a
// modified click keeps the browser's own choice.
function officeInNewTab(e: LinkClick, id: string, mime: string | undefined): void {
  if (!plainClick(e) || kindOf({ kind: "file", mime_type: mime }) !== "office") return;
  e.preventDefault();
  window.open(`/n/${encodeURIComponent(id)}`, "_blank", "noopener");
}

// RowActions are the icons of one row: download, move to the trash (or restore, for a file in the trash), and More. Each
// button's name starts with its tooltip's verb and names its file ("Download: name"), so a screen reader and a sighted
// person hear the same action (decision 68).
function RowActions({ file, onTrash, onRestore }: { file: LargestFile; onTrash: (id: string) => void; onRestore: (id: string) => void }) {
  const { t } = useT();
  const menu: MenuProps = {
    items: [
      {
        key: "open",
        label: (
          <a href={`/n/${encodeURIComponent(file.id)}`} onClick={(e) => officeInNewTab(e, file.id, file.mime_type)}>
            {t("files.act.open")}
          </a>
        ),
      },
    ],
  };
  return (
    <span style={{ display: "inline-flex", alignItems: "center", justifyContent: "flex-end", gap: 2 }}>
      <Tooltip title={t("files.act.download")}>
        <Button
          type="text"
          shape="circle"
          href={contentPath(file.id)}
          data-first-action
          aria-label={t("storage.act.downloadFor", { name: file.name })}
          icon={<Download size={18} aria-hidden="true" />}
        />
      </Tooltip>
      {file.in_trash ? (
        <Tooltip title={t("trash.restore")}>
          <Button
            type="text"
            shape="circle"
            aria-label={t("storage.act.restoreFor", { name: file.name })}
            icon={<RotateCcw size={18} aria-hidden="true" />}
            onClick={() => onRestore(file.id)}
          />
        </Tooltip>
      ) : (
        <Tooltip title={t("files.act.trash")}>
          <Button
            type="text"
            shape="circle"
            aria-label={t("storage.act.trashFor", { name: file.name })}
            icon={<Trash2 size={18} aria-hidden="true" />}
            onClick={() => onTrash(file.id)}
          />
        </Tooltip>
      )}
      <Tooltip title={t("files.selection.more")}>
        <Dropdown menu={menu} trigger={["click"]}>
          <Button
            type="text"
            shape="circle"
            aria-label={t("storage.act.moreFor", { name: file.name })}
            icon={<MoreHorizontal size={18} aria-hidden="true" />}
          />
        </Dropdown>
      </Tooltip>
    </span>
  );
}
