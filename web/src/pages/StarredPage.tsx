// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Skeleton } from "antd";
import { StarOff } from "lucide-react";
import Listing, { type ListColumn } from "../components/files/Listing";
import Icon from "../components/files/Icon";
import DriveName, { openDrive } from "../components/drive/DriveName";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatBytes, formatDate } from "../lib/files/format";
import { useLocationSpaces } from "../lib/drive/spaces";
import { useLoadMoreSentinel } from "../lib/files/loadMoreSentinel";
import DriveTextFailure from "../components/drive/DriveTextFailure";
import { kindOf } from "../lib/files/kind";
import { listStarred, unstarNode, type StarredItem } from "../lib/stars";
import { useDriveText } from "../lib/drive/driveText";
import { locationOf } from "../lib/drive/location";

// Starred: the items the caller starred, newest star first, on the one table (tl4, decision 137). Removing a star takes the item
// off this list only; the item itself is not changed. Several stars are removed in one bulk action. A star on an item the caller
// can no longer reach is kept but not listed. A failed first load shows its error and no table. Every change is watched for a
// change of session, so an answer for one person never reaches the next.
const PAGE = 20;

export default function StarredPage() {
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
  const [items, setItems] = useState<StarredItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // busy lists the items whose star is being removed. The ref answers a second click before the state has changed.
  const [busy, setBusy] = useState<string[]>([]);
  const inFlight = useRef(new Set<string>());

  const load = useCallback(
    async (reset: boolean, from: string | null) => {
      const changed = sessionChanged();
      setLoading(true);
      try {
        const page = await listStarred(PAGE, reset ? null : from);
        if (changed()) return;
        setItems((prev) => (reset ? page.items : [...prev, ...page.items.filter((i) => !prev.some((s) => s.id === i.id))]));
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
    document.title = `${t("nav.starred")} · ${brandName}`;
  }, [t, brandName]);

  // A new session starts with nothing shown, then reads its own list.
  useEffect(() => {
    if (!signedIn) return;
    setItems([]);
    setCursor(null);
    setError(null);
    void load(true, null);
    // The session key is the trigger: a new person reads again.
  }, [sk, signedIn, load]);

  // unstarMany removes the stars of the given items, one after another. An item whose star goes is taken off the list at once; a
  // failure shows its error and keeps the items it did not reach.
  const unstarMany = async (ids: string[]) => {
    const changed = sessionChanged();
    for (const id of ids) {
      if (inFlight.current.has(id)) continue;
      inFlight.current.add(id);
      setBusy((b) => [...b, id]);
      try {
        await unstarNode(id);
        if (changed()) return;
        setItems((prev) => prev.filter((i) => i.id !== id));
        setError(null);
      } catch (err) {
        if (!changed() && !signingOut()) setError(fileErrorText(err, langRef.current));
        return;
      } finally {
        inFlight.current.delete(id);
        setBusy((b) => b.filter((x) => x !== id));
      }
    }
  };

  const sentinel = useLoadMoreSentinel(cursor !== null, loading, () => void load(false, cursor));
  const columns: ListColumn<StarredItem>[] = [
    { key: "name", title: t("files.col.name"), render: (n) => <DriveName node={n} /> },
    { key: "where", title: t("drive.col.location"), secondary: true, render: (n) => locationOf(n, spaces, t("nav.files")) },
    {
      key: "when",
      title: t("drive.col.starredOn"),
      secondary: true,
      ariaSort: "descending",
      render: (n) => <time dateTime={n.starred_at}>{formatDate(n.starred_at, lang)}</time>,
    },
    { key: "size", title: t("files.col.size"), secondary: true, align: "end", render: (n) => (n.kind === "dir" ? "—" : formatBytes(n.size, lang)) },
  ];

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("nav.starred")}</h1>

      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}

      {drive.failed && <DriveTextFailure retry={drive.retry} />}
      {((!driveReady && !drive.failed) || (loading && items.length === 0)) && error === null && <Skeleton active />}

      {driveReady && (items.length > 0 || (!loading && error === null)) && (
        <div className="items">
          <Listing<StarredItem>
            label={t("nav.starred")}
            items={items}
            rowLabel={(n) => n.name}
            columns={columns}
            icon={(n) => <Icon kind={kindOf(n)} size={24} node={n} />}
            selectable
            bulk={(ids) => [{ key: "unstar", label: t("starred.remove"), icon: <StarOff size={18} aria-hidden="true" focusable="false" />, enabled: true, run: () => void unstarMany(ids) }]}
            actions={(n) => [
              {
                key: "unstar",
                label: `${t("starred.remove")}: ${n.name}`,
                icon: <StarOff size={18} aria-hidden="true" focusable="false" />,
                disabled: busy.includes(n.id),
                run: () => void unstarMany([n.id]),
              },
            ]}
            menu={(n) => [{ key: "open", label: t("files.act.open"), onSelect: () => openDrive(n) }]}
            onOpen={(n) => openDrive(n)}
            empty={{ title: t("drive.empty.starred"), description: t("starred.empty"), className: "empty" }}
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
    </div>
  );
}
