// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, type ReactNode } from "react";
import { Alert, theme } from "antd";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { isAdmin, session, sessionKey } from "../lib/session";
import { queryKey } from "../lib/query";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatDate } from "../lib/files/format";
import { listAccessRequests } from "../lib/sharing/api";
import { myStorage, refreshMyStorage } from "../lib/storage/api";
import { heldBytes } from "../lib/storage/logic";
import { HOME_SECTION_SIZE, storageNoticePercent, waitingCount } from "../lib/home";
import { activityKey, lastActivity, type RecentItem } from "../lib/recent";
import { listRecent, listSharedRecent, listStarred, type SharedItem, type StarredItem } from "../lib/stars";
import NodeRow from "../components/home/NodeRow";
import UsageMeter from "../components/UsageMeter";

// Home: the caller's recent items, starred items, what was shared with it lately, the requests waiting for its decision,
// and a notice when its own storage is nearly full. Each section reads its own list. A section whose first load fails
// shows its error and no empty state; a section whose refetch fails keeps the list it had, with the error beside it.
// An installation admin has no drive: Home makes no request for it.
// Each read's key starts with the session it was made under (queryKey), so a list from one person never shows for the next.

interface ListingProps<T> {
  id: string;
  title: string;
  seeAllHref: string;
  query: UseQueryResult<T[]>;
  emptyKey: string;
  row: (item: T) => ReactNode;
}

function Listing<T extends { id: string }>({ id, title, seeAllHref, query, emptyKey, row }: ListingProps<T>) {
  const { t } = useT();
  const lang = useLang();
  const { token } = theme.useToken();
  const failed = query.isError ? fileErrorText(query.error, lang) : null;
  const items = query.data ?? [];
  return (
    <section style={{ marginBlockEnd: 20 }} aria-labelledby={id}>
      <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", gap: 8, marginBlockEnd: 8 }}>
        <h2 id={id} style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
          {title}
        </h2>
        <a href={seeAllHref}>{t("home.seeAll")}</a>
      </div>
      {failed !== null && <Alert type="error" showIcon role="alert" message={failed} style={{ marginBlockEnd: 8 }} />}
      {query.isSuccess && items.length === 0 && failed === null && (
        <p role="status" className="empty" style={{ margin: 0, fontSize: token.fontSizeSM, color: token.colorTextSecondary }}>
          {t(emptyKey)}
        </p>
      )}
      {items.length > 0 && (
        <ul className="items" aria-label={title} style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
          {items.map((item) => row(item))}
        </ul>
      )}
    </section>
  );
}

export default function HomePage() {
  const { t, tp } = useT();
  const lang = useLang();
  const { token } = theme.useToken();
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const my = useStore(myStorage);
  const active = me !== null && !isAdmin(me.user);
  const sk = sessionKey(me);

  const recent = useQuery({
    queryKey: queryKey("home", "recent"),
    queryFn: async () => (await listRecent(HOME_SECTION_SIZE)).items,
    enabled: active,
    refetchOnMount: "always",
  });
  const starred = useQuery({
    queryKey: queryKey("home", "starred"),
    queryFn: async () => (await listStarred(HOME_SECTION_SIZE)).items,
    enabled: active,
    refetchOnMount: "always",
  });
  const shared = useQuery({
    queryKey: queryKey("home", "shared"),
    queryFn: async () => (await listSharedRecent(HOME_SECTION_SIZE)).items,
    enabled: active,
    refetchOnMount: "always",
  });
  const waiting = useQuery({
    queryKey: queryKey("home", "waiting"),
    queryFn: async () => (await listAccessRequests()).items,
    enabled: active,
    refetchOnMount: "always",
  });

  useEffect(() => {
    document.title = `${t("nav.home")} · ${brandName}`;
  }, [t, brandName]);

  // The storage figures are read on each mount of Home, as Svelte does, for the notice and the meter.
  useEffect(() => {
    if (!active) return;
    void refreshMyStorage().catch(() => undefined);
  }, [active, sk]);

  const percent = storageNoticePercent(my);
  const waitingItems = waiting.data ?? [];

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("nav.home")}</h1>

      {percent !== null && my !== null && (
        <section
          aria-labelledby="home-storage"
          style={{ display: "grid", gap: 8, marginBlockEnd: 20, padding: 12, fontSize: 14, background: token.colorWarningBg, borderRadius: token.borderRadius }}
        >
          <h2 id="home-storage" style={{ position: "absolute", width: 1, height: 1, overflow: "hidden", clip: "rect(0 0 0 0)" }}>
            {t("home.storageLink")}
          </h2>
          <p style={{ margin: 0 }}>
            {t("home.storageNotice", { percent: String(percent) })} <a href="/storage">{t("home.storageLink")}</a>
          </p>
          <UsageMeter used={heldBytes(my)} quota={my.quota_bytes} />
        </section>
      )}

      {waitingItems.length > 0 && (
        <section aria-labelledby="home-waiting" style={{ marginBlockEnd: 20 }}>
          <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", gap: 8, marginBlockEnd: 8 }}>
            <h2 id="home-waiting" style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
              {t("home.waiting")}
            </h2>
            <a href="/requests">{t("home.seeAll")}</a>
          </div>
          <p role="status" style={{ margin: 0 }}>
            {tp("home.waiting.count", waitingCount(waitingItems))}
          </p>
        </section>
      )}
      {waiting.isError && <Alert type="error" showIcon role="alert" message={fileErrorText(waiting.error, lang)} style={{ marginBlockEnd: 12 }} />}

      {active && (
        <>
      <Listing<RecentItem>
        id="home-recent"
        title={t("home.recent")}
        seeAllHref="/recent"
        query={recent}
        emptyKey="recent.empty"
        row={(n) => {
          const act = lastActivity(n);
          return <NodeRow key={n.id} node={n} meta={t(activityKey(act), { date: formatDate(act.at, lang) })} />;
        }}
      />

      <Listing<StarredItem>
        id="home-starred"
        title={t("home.starred")}
        seeAllHref="/starred"
        query={starred}
        emptyKey="starred.empty"
        row={(n) => <NodeRow key={n.id} node={n} meta={t("starred.on", { date: formatDate(n.starred_at, lang) })} />}
      />

      <Listing<SharedItem>
        id="home-shared"
        title={t("home.shared")}
        seeAllHref="/shared"
        query={shared}
        emptyKey="shared.empty"
        row={(n) => <NodeRow key={n.id} node={n} meta={t("home.sharedOn", { date: formatDate(n.shared_at, lang) })} />}
      />
        </>
      )}
    </div>
  );
}
