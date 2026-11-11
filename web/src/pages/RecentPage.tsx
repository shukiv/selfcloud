// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Skeleton } from "antd";
import { Eraser, X } from "lucide-react";
import Listing, { type ListColumn } from "../components/files/Listing";
import Icon from "../components/files/Icon";
import IconButton from "../components/IconButton";
import DriveName, { openDrive } from "../components/drive/DriveName";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatDate } from "../lib/files/format";
import { useLocationSpaces } from "../lib/drive/spaces";
import { kindOf } from "../lib/files/kind";
import { activityKey, lastActivity, mergeRecent, type RecentItem } from "../lib/recent";
import { clearRecent, listRecent, removeRecent } from "../lib/stars";
import { useDriveText } from "../lib/drive/driveText";
import { locationOf } from "../lib/drive/location";
import { useLoadMoreSentinel } from "../lib/files/loadMoreSentinel";
import DriveTextFailure from "../components/drive/DriveTextFailure";

// Recent: the items the caller opened or changed lately, most recent first, on the one table (tl4, decision 137). It is private to
// the caller and kept for 30 days, at most 500 items. Removing one takes it off this list only; clearing empties the whole list
// after the person confirms, and changes no file. A failed first load shows its error and no table. Every request is watched for a
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
  const spaces = useLocationSpaces(signedIn);
  const drive = useDriveText();
  const driveReady = drive.ready;
  const [items, setItems] = useState<RecentItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // moreFailed: the last next page failed, so the sentinel waits for Load more.
  const [moreFailed, setMoreFailed] = useState(false);
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
        setMoreFailed(false);
      } catch (err) {
        // A failed next page is not asked for again by the sentinel; Load more tries again.
        if (!reset) setMoreFailed(true);
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

  const sentinel = useLoadMoreSentinel(cursor !== null, loading, moreFailed, () => void load(false, cursor));
  const columns: ListColumn<RecentItem>[] = [
    { key: "name", title: t("files.col.name"), render: (n) => <DriveName node={n} /> },
    { key: "where", title: t("drive.col.location"), secondary: true, render: (n) => locationOf(n, spaces, t("nav.files")) },
    {
      key: "opened",
      title: t("drive.col.opened"),
      secondary: true,
      ariaSort: "descending",
      render: (n) => {
        const act = lastActivity(n);
        return <time dateTime={act.at}>{t(activityKey(act), { date: formatDate(act.at, lang) })}</time>;
      },
    },
  ];

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: "8px 12px", marginBlockEnd: 16 }}>
        <h1 style={{ margin: 0, fontSize: 20, fontWeight: 600 }}>{t("nav.recent")}</h1>
        {items.length > 0 && (
          <IconButton label={t("recent.clear")} onClick={() => setAskClear(true)} icon={<Eraser size={18} aria-hidden="true" focusable="false" />} />
        )}
      </div>

      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}

      {drive.failed && <DriveTextFailure retry={drive.retry} />}
      {((!driveReady && !drive.failed) || (loading && items.length === 0)) && error === null && <Skeleton active />}

      {driveReady && (items.length > 0 || (!loading && error === null)) && (
        <div className="items">
          <Listing<RecentItem>
            label={t("nav.recent")}
            items={items}
            rowLabel={(n) => n.name}
            columns={columns}
            icon={(n) => <Icon kind={kindOf(n)} size={24} node={n} />}
            actions={(n) => [
              {
                key: "remove",
                label: t("recent.removeName", { name: n.name }),
                icon: <X size={18} aria-hidden="true" focusable="false" />,
                disabled: busy.includes(n.id),
                run: () => void remove(n),
              },
            ]}
            menu={(n) => [{ key: "open", label: t("files.act.open"), onSelect: () => openDrive(n) }]}
            onOpen={(n) => openDrive(n)}
            empty={{ title: t("drive.empty.recent"), description: t("recent.empty"), className: "empty" }}
          />
        </div>
      )}

      {cursor !== null && <div ref={sentinel} aria-hidden="true" />}
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
