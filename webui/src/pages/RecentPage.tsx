// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Skeleton } from "antd";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatDate } from "../lib/files/format";
import { activityKey, lastActivity, mergeRecent, type RecentItem } from "../lib/recent";
import { clearRecent, listRecent, removeRecent } from "../lib/stars";
import NodeRow from "../components/home/NodeRow";

// Recent: the items the caller opened or changed lately, most recent first. It is private to the caller and kept for 30
// days, at most 500 items. Removing one takes it off this list only; clearing empties the whole list after the person
// confirms, and changes no file. A failed first load shows its error and no empty state. Every request is watched for a
// change of session, and a double click sends one request.
const PAGE = 20;

export default function RecentPage() {
  const { t } = useT();
  const lang = useLang();
  // The language is read through a ref: a change of language must not clear the list and read it again.
  const langRef = useRef(lang);
  langRef.current = lang;
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const signedIn = me !== null;
  const sk = sessionKey(me);
  const [items, setItems] = useState<RecentItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string[]>([]);
  const inFlight = useRef(new Set<string>());
  const [askClear, setAskClear] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [clearError, setClearError] = useState<string | null>(null);
  const clearFlight = useRef(false);

  const load = useCallback(
    async (reset: boolean, from: string | null) => {
      const changed = sessionChanged();
      setLoading(true);
      try {
        const page = await listRecent(PAGE, reset ? null : from);
        if (changed()) return;
        setItems((prev) => (reset ? page.items : mergeRecent(prev, page.items)));
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
    document.title = `${t("nav.recent")} · ${brandName}`;
  }, [t, brandName]);

  // A new session starts with nothing shown, then reads its own list.
  useEffect(() => {
    if (!signedIn) return;
    setItems([]);
    setCursor(null);
    setError(null);
    void load(true, null);
  }, [sk, signedIn, load]);

  const remove = async (n: RecentItem) => {
    if (inFlight.current.has(n.id)) return;
    inFlight.current.add(n.id);
    setBusy((b) => [...b, n.id]);
    const changed = sessionChanged();
    try {
      await removeRecent(n.id);
      if (changed()) return;
      setItems((prev) => prev.filter((i) => i.id !== n.id));
      setError(null);
    } catch (err) {
      if (!changed() && !signingOut()) setError(fileErrorText(err, langRef.current));
    } finally {
      inFlight.current.delete(n.id);
      setBusy((b) => b.filter((x) => x !== n.id));
    }
  };

  // clearAll empties the caller's Recent list after the person has confirmed. A failed request keeps the dialog open.
  const clearAll = async () => {
    if (clearFlight.current) return;
    clearFlight.current = true;
    setClearing(true);
    setClearError(null);
    const changed = sessionChanged();
    try {
      await clearRecent();
      if (changed()) return;
      setItems([]);
      setCursor(null);
      setAskClear(false);
      setError(null);
    } catch (err) {
      if (!changed() && !signingOut()) setClearError(fileErrorText(err, langRef.current));
    } finally {
      clearFlight.current = false;
      setClearing(false);
    }
  };

  const closeClear = () => {
    if (clearing) return;
    setAskClear(false);
    setClearError(null);
  };

  const empty = !loading && items.length === 0 && error === null;

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: "8px 12px", marginBlockEnd: 16 }}>
        <h1 style={{ margin: 0, fontSize: 20, fontWeight: 600 }}>{t("nav.recent")}</h1>
        {items.length > 0 && <Button onClick={() => setAskClear(true)}>{t("recent.clear")}</Button>}
      </div>

      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}

      {loading && items.length === 0 && error === null && <Skeleton active />}

      {empty && (
        <p role="status" className="empty" style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
          {t("recent.empty")}
        </p>
      )}

      {items.length > 0 && (
        <ul className="items" aria-label={t("nav.recent")} style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
          {items.map((n) => {
            const act = lastActivity(n);
            return (
              <NodeRow
                key={n.id}
                node={n}
                meta={t(activityKey(act), { date: formatDate(act.at, lang) })}
                action={
                  <Button
                    disabled={busy.includes(n.id)}
                    aria-label={t("recent.removeName", { name: n.name })}
                    onClick={() => void remove(n)}
                  >
                    {t("recent.remove")}
                  </Button>
                }
              />
            );
          })}
        </ul>
      )}

      {cursor !== null && (
        <div style={{ marginBlockStart: 12 }}>
          <Button disabled={loading} onClick={() => void load(false, cursor)}>
            {t("files.loadMore")}
          </Button>
        </div>
      )}

      <Modal
        open={askClear}
        title={t("recent.clearTitle")}
        okText={t("recent.clearAction")}
        okButtonProps={{ danger: true, disabled: clearing }}
        cancelText={t("common.cancel")}
        cancelButtonProps={{ disabled: clearing }}
        maskClosable={false}
        onOk={() => void clearAll()}
        onCancel={closeClear}
      >
        <p>{t("recent.clearMessage")}</p>
        {clearError !== null && <Alert type="error" showIcon role="alert" message={clearError} />}
      </Modal>
    </div>
  );
}
