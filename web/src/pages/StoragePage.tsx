// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Skeleton, Tag } from "antd";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { isAdmin, session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { formatBytes, formatDate } from "../lib/files/format";
import { fileErrorText } from "../lib/files/fileErrors";
import { myLargest, myStorage, refreshMyStorage, type LargestFile } from "../lib/storage/api";
import { availableOf, heldBytes, LARGEST_PAGE, mergePage } from "../lib/storage/logic";
import UsageMeter from "../components/UsageMeter";

// Storage: the caller's own My files, what they hold against their limit, and the largest files in them. Team spaces
// are not here; they count against the account, and the owner sees them in Manage > Storage. An installation admin has
// no drive, so this page reads nothing for it. A failed first load shows its error and no empty state.
export default function StoragePage() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const my = useStore(myStorage);
  const signedIn = me !== null;
  const admin = me !== null && isAdmin(me.user);
  const sk = sessionKey(me);
  const [files, setFiles] = useState<LargestFile[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // figuresError is a failed read of the storage figures: the meter is not drawn then, and the error stands in its place.
  const [figuresError, setFiguresError] = useState<string | null>(null);
  // The language is read through a ref: a change of language must not clear the list and read it again.
  const langRef = useRef(lang);
  langRef.current = lang;

  const load = useCallback(
    async (reset: boolean, from: string | null) => {
      const changed = sessionChanged();
      setLoading(true);
      try {
        const page = await myLargest(LARGEST_PAGE, reset ? null : from);
        if (changed()) return;
        setFiles((prev) => (reset ? page.items : mergePage(prev, page.items)));
        setCursor(page.next_cursor ?? null);
        setError(null);
      } catch (err) {
        if (!changed() && !signingOut()) setError(fileErrorText(err, langRef.current));
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  useEffect(() => {
    document.title = `${t("storage.title")} · ${brandName}`;
  }, [t, brandName]);

  // Each mount reads the storage figures and then the largest files, for the person who is signed in.
  useEffect(() => {
    if (!signedIn || admin) return;
    setFiles([]);
    setCursor(null);
    setError(null);
    setFiguresError(null);
    const changed = sessionChanged();
    void (async () => {
      try {
        await refreshMyStorage();
      } catch (err) {
        if (!changed()) setFiguresError(fileErrorText(err, langRef.current));
      }
      if (changed()) return;
      await load(true, null);
    })();
  }, [sk, signedIn, admin, load]);

  if (admin) return null;

  const held = my ? heldBytes(my) : 0;
  const available = my ? availableOf(held, my.quota_bytes) : null;
  const empty = !loading && files.length === 0 && error === null;

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("storage.title")}</h1>

      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}
      {figuresError !== null && <Alert type="error" showIcon role="alert" message={figuresError} style={{ marginBlockEnd: 12 }} />}

      {my && figuresError === null && (
        <section aria-labelledby="storage-meter" style={{ display: "grid", gap: 12, marginBlockEnd: 20 }}>
          <h2 id="storage-meter" style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
            {t("storage.mine")}
          </h2>
          <UsageMeter used={held} quota={my.quota_bytes} />
          <dl style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", gap: 12, margin: 0 }}>
            <div>
              <dt style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("storage.used")}</dt>
              <dd style={{ margin: 0 }}>{formatBytes(my.used_bytes, lang)}</dd>
            </div>
            <div>
              <dt style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("storage.reserved")}</dt>
              <dd style={{ margin: 0 }}>{formatBytes(my.reserved_bytes, lang)}</dd>
            </div>
            <div>
              <dt style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("storage.limit")}</dt>
              <dd style={{ margin: 0 }}>{my.quota_bytes === null ? t("storage.noLimit") : formatBytes(my.quota_bytes, lang)}</dd>
            </div>
            {available !== null && (
              <div>
                <dt style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("storage.available")}</dt>
                <dd style={{ margin: 0 }}>{formatBytes(available, lang)}</dd>
              </div>
            )}
          </dl>
        </section>
      )}

      <section aria-labelledby="largest-title">
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "baseline", justifyContent: "space-between", gap: "8px 12px", marginBlockEnd: 8 }}>
          <h2 id="largest-title" style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
            {t("storage.largest")}
          </h2>
          <a href="/trash">{t("storage.trashLink")}</a>
        </div>

        {loading && files.length === 0 && error === null && <Skeleton active />}

        {empty && (
          <p role="status" className="empty" style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
            {t("storage.largestEmpty")}
          </p>
        )}

        {files.length > 0 && (
          // The table scrolls inside its box at phone width; the page itself never scrolls sideways.
          <div style={{ overflowX: "auto" }}>
            <table className="storage-table" style={{ width: "100%", borderCollapse: "collapse", fontSize: 14 }}>
              <thead>
                <tr>
                  <th scope="col" style={{ textAlign: "start", padding: 8 }}>{t("storage.name")}</th>
                  <th scope="col" style={{ textAlign: "end", padding: 8 }}>{t("storage.size")}</th>
                  <th scope="col" style={{ textAlign: "end", padding: 8 }}>{t("storage.oldVersions")}</th>
                  <th scope="col" style={{ textAlign: "start", padding: 8 }}>{t("storage.modified")}</th>
                </tr>
              </thead>
              <tbody>
                {files.map((f) => (
                  <tr key={f.id} style={{ borderBlockStart: "1px solid var(--ant-color-border-secondary)" }}>
                    <th scope="row" style={{ textAlign: "start", padding: 8, fontWeight: 600, overflowWrap: "anywhere" }}>
                      <a href={`/n/${encodeURIComponent(f.id)}`}>
                        <bdi>{f.name}</bdi>
                      </a>{" "}
                      {f.in_trash && <Tag>{t("storage.inTrash")}</Tag>}
                    </th>
                    <td style={{ textAlign: "end", padding: 8, fontVariantNumeric: "tabular-nums" }}>{formatBytes(f.size, lang)}</td>
                    <td style={{ textAlign: "end", padding: 8, fontVariantNumeric: "tabular-nums" }}>{formatBytes(f.versions_bytes, lang)}</td>
                    <td style={{ textAlign: "start", padding: 8 }}>
                      <time dateTime={f.modified_at}>{formatDate(f.modified_at, lang)}</time>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {cursor !== null && (
          <div style={{ marginBlockStart: 12 }}>
            <Button disabled={loading} onClick={() => void load(false, cursor)}>
              {t("common.loadMore")}
            </Button>
          </div>
        )}
      </section>
    </div>
  );
}
