// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Skeleton } from "antd";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatDate } from "../lib/files/format";
import { listStarred, unstarNode, type StarredItem } from "../lib/stars";
import NodeRow from "../components/home/NodeRow";

// Starred: the items the caller starred, newest star first. Removing a star takes the item off this list only; the item
// itself is not changed. A star on an item the caller can no longer reach is kept but not listed. A failed first load
// shows its error and no empty state. Every change is watched for a change of session, so an answer for one person
// never reaches the next.
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

  const unstar = async (n: StarredItem) => {
    if (inFlight.current.has(n.id)) return;
    inFlight.current.add(n.id);
    setBusy((b) => [...b, n.id]);
    const changed = sessionChanged();
    try {
      await unstarNode(n.id);
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

  const empty = !loading && items.length === 0 && error === null;

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("nav.starred")}</h1>

      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}

      {loading && items.length === 0 && error === null && <Skeleton active />}

      {empty && (
        <p role="status" className="empty" style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
          {t("starred.empty")}
        </p>
      )}

      {items.length > 0 && (
        <ul className="items" aria-label={t("nav.starred")} style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
          {items.map((n) => (
            <NodeRow
              key={n.id}
              node={n}
              meta={t("starred.on", { date: formatDate(n.starred_at, lang) })}
              action={
                <Button disabled={busy.includes(n.id)} onClick={() => void unstar(n)}>
                  {t("starred.remove")}
                </Button>
              }
            />
          ))}
        </ul>
      )}

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
