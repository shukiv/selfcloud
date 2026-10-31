// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo } from "react";
import { Alert, Button, Skeleton, Tag } from "antd";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session } from "../lib/session";
import { queryKey } from "../lib/query";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatDate } from "../lib/files/format";
import { kindOf } from "../lib/files/kind";
import { sharedWithMe } from "../lib/sharing/api";
import type { Node, Page } from "../lib/files/types";
import Icon from "../components/files/Icon";
import KindMarks from "../components/files/KindMarks";

// Shared with me: the items other people shared with the caller, each at the role it has, in the order the server
// gives them. An item opens in its folder, through its address (/n/{nod}), like any link a share mail carries.
// A failed refresh or a failed load-more keeps the list on screen and shows the failure above it.
export default function SharedPage() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const signedIn = me !== null;

  const q = useInfiniteQuery({
    queryKey: queryKey("shared"),
    queryFn: ({ pageParam }: { pageParam: string | undefined }) => sharedWithMe(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<Node>) => last.next_cursor,
    enabled: signedIn,
    refetchOnMount: "always",
  });

  const items = useMemo<Node[]>(() => (q.data?.pages ?? []).flatMap((p) => p.items), [q.data]);
  const hasMore = q.hasNextPage === true;
  const hasData = q.data !== undefined;

  useEffect(() => {
    document.title = `${t("nav.shared")} · ${brandName}`;
  }, [t, brandName]);

  // ownerText names the owner when it is the caller; otherwise the API gives no name, and nothing is shown.
  const ownerText = (n: Node): string =>
    n.owner_id !== undefined && me !== null && n.owner_id === me.user.id ? t("files.details.you") : "";

  const failure = q.isError ? fileErrorText(q.error, lang) : null;

  return (
    <div>
      <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>
        {t("nav.shared")}
      </h1>

      {failure !== null && (
        <Alert type={hasData ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />
      )}

      {!hasData && q.isPending && signedIn && <Skeleton active />}

      {hasData && items.length === 0 && failure === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>{t("shared.empty")}</p>
      )}

      {items.length > 0 && (
        <ul className="shared" aria-label={t("nav.shared")} style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
          {items.map((n) => {
            const owner = ownerText(n);
            return (
              <li
                key={n.id}
                style={{
                  display: "flex",
                  flexWrap: "wrap",
                  alignItems: "center",
                  gap: "8px 12px",
                  minHeight: 44,
                  padding: "8px 12px",
                  border: "1px solid var(--ant-color-border)",
                  borderRadius: 4,
                }}
              >
                <Icon kind={kindOf(n)} size={24} />
                <a href={`/n/${n.id}`} style={{ flex: "1 1 160px", minWidth: 0, overflowWrap: "anywhere", fontWeight: 600 }}>
                  <bdi>{n.name}</bdi>
                </a>
                <KindMarks node={n} />
                <Tag>{t(`role.${n.role ?? "viewer"}`)}</Tag>
                <span style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
                  {t("shared.lastChange", { date: formatDate(n.modified_at, lang) })}
                </span>
                {owner !== "" && <span style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{owner}</span>}
              </li>
            );
          })}
        </ul>
      )}

      {hasMore && (
        <div style={{ display: "flex", justifyContent: "center", padding: "16px 0" }}>
          <Button onClick={() => void q.fetchNextPage()} loading={q.isFetchingNextPage}>
            {t("files.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}
