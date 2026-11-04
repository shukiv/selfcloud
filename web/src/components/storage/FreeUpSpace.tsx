// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button } from "antd";
import { History, Trash2 } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { freeUpCards, type Breakdown } from "../../lib/storage/breakdown";

// FreeUpSpace is the "Free up space" pair of cards (decision 79): the trash, which counts until it is emptied, and the old
// versions. A card with nothing to free is not shown, and the section is not shown when neither has anything. The old
// versions card has no action yet: there is no page that reviews them (the owner decides, see decision 79).
export default function FreeUpSpace({ breakdown }: { breakdown: Breakdown }) {
  const { t } = useT();
  const lang = useLang();
  const show = freeUpCards(breakdown);
  if (!show.trash && !show.versions) return null;

  return (
    <section aria-labelledby="free-up-title" className="storage-free">
      <h2 id="free-up-title" style={{ margin: "0 0 12px", fontSize: 16, fontWeight: 600 }}>
        {t("storage.free.title")}
      </h2>
      <div className="storage-free-cards" style={{ display: "grid", gap: 16 }}>
        {show.trash && (
          <article className="storage-card storage-free-card" aria-labelledby="free-trash">
            <div style={{ display: "flex", gap: 14, alignItems: "flex-start" }}>
              <span className="storage-free-icon" aria-hidden="true">
                <Trash2 size={20} />
              </span>
              <div style={{ flex: 1, minWidth: 0, display: "grid", gap: 4 }}>
                <strong id="free-trash" style={{ fontSize: 15 }}>
                  {t("storage.free.trashTitle")}
                </strong>
                <span style={{ color: "var(--ant-color-text-secondary)" }}>
                  {t("storage.free.trashText", {
            size: formatBytes(breakdown.trash_bytes, lang),
            files: t("storage.fileCount", { count: breakdown.trash_files }),
          })}
                </span>
              </div>
            </div>
            <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 12 }}>
              <Button href="/trash" icon={<Trash2 size={16} aria-hidden="true" />}>
                {t("storage.trashLink")}
              </Button>
            </div>
          </article>
        )}
        {show.versions && (
          <article className="storage-card storage-free-card" aria-labelledby="free-versions">
            <div style={{ display: "flex", gap: 14, alignItems: "flex-start" }}>
              <span className="storage-free-icon" aria-hidden="true">
                <History size={20} />
              </span>
              <div style={{ flex: 1, minWidth: 0, display: "grid", gap: 4 }}>
                <strong id="free-versions" style={{ fontSize: 15 }}>
                  {t("storage.oldVersions")}
                </strong>
                <span style={{ color: "var(--ant-color-text-secondary)" }}>
                  {t("storage.free.versionsText", {
            size: formatBytes(breakdown.versions_bytes, lang),
            files: t("storage.fileCount", { count: breakdown.versions_files }),
          })}
                </span>
              </div>
            </div>
          </article>
        )}
      </div>
    </section>
  );
}
