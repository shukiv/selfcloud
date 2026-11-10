// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo } from "react";
import { Alert, Button, Skeleton, Tag } from "antd";
import { useInfiniteQuery } from "@tanstack/react-query";
import Listing, { type ListColumn } from "../components/files/Listing";
import Icon from "../components/files/Icon";
import DriveName, { openDrive } from "../components/drive/DriveName";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session } from "../lib/session";
import { queryKey } from "../lib/query";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatDate } from "../lib/files/format";
import { kindOf } from "../lib/files/kind";
import { sharedWithMe, type SharedNode } from "../lib/sharing/api";
import type { Page } from "../lib/files/types";
import { useDriveText } from "../lib/drive/driveText";
import { useLoadMoreSentinel } from "../lib/files/loadMoreSentinel";
import DriveTextFailure from "../components/drive/DriveTextFailure";

// Shared with me: the items other people shared with the caller, each at the role it has, on the one table (tl4, decision 137), in
// the order the server gives them, newest share first. An item opens through its address (/n/{nod}), like any link a share mail
// carries. A failed refresh or a failed load-more keeps the list on screen and shows the failure above it.
export default function SharedPage() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const signedIn = me !== null;
  const drive = useDriveText();
  const driveReady = drive.ready;

  const q = useInfiniteQuery({
    queryKey: queryKey("shared"),
    queryFn: ({ pageParam }: { pageParam: string | undefined }) => sharedWithMe(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<SharedNode>) => last.next_cursor,
    enabled: signedIn,
    refetchOnMount: "always",
  });

  const items = useMemo<SharedNode[]>(() => (q.data?.pages ?? []).flatMap((p) => p.items), [q.data]);
  const hasMore = q.hasNextPage === true;
  const sentinel = useLoadMoreSentinel(hasMore, q.isFetchingNextPage, q.isFetchNextPageError, () => void q.fetchNextPage());
  const hasData = q.data !== undefined;

  useEffect(() => {
    document.title = `${t("nav.shared")} · ${brandName}`;
  }, [t, brandName]);

  // ownerText names the owner: "You" when it is the caller, otherwise the name the API gives, or nothing when it gives none.
  const ownerText = (n: SharedNode): string => (n.owner_id !== undefined && me !== null && n.owner_id === me.user.id ? t("files.details.you") : (n.owner_name ?? ""));

  const failure = q.isError ? fileErrorText(q.error, lang) : null;

  const columns: ListColumn<SharedNode>[] = [
    { key: "name", title: t("files.col.name"), render: (n) => <DriveName node={n} /> },
    { key: "by", title: t("drive.col.sharedBy"), secondary: true, render: (n) => ownerText(n) },
    { key: "role", title: t("drive.col.yourRole"), secondary: true, render: (n) => <Tag>{t(`role.${n.role ?? "viewer"}`)}</Tag> },
    {
      key: "when",
      title: t("drive.col.sharedOn"),
      secondary: true,
      ariaSort: "descending",
      render: (n) => (n.shared_at ? <time dateTime={n.shared_at}>{formatDate(n.shared_at, lang)}</time> : ""),
    },
  ];

  return (
    <div>
      <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>
        {t("nav.shared")}
      </h1>

      {failure !== null && <Alert type={hasData ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {drive.failed && <DriveTextFailure retry={drive.retry} />}
      {((!driveReady && !drive.failed) || (!hasData && q.isPending && signedIn)) && <Skeleton active />}

      {driveReady && hasData && (items.length > 0 || failure === null) && (
        <div className="shared">
          <Listing<SharedNode>
            label={t("nav.shared")}
            items={items}
            rowLabel={(n) => n.name}
            columns={columns}
            icon={(n) => <Icon kind={kindOf(n)} size={24} node={n} />}
            menu={(n) => [{ key: "open", label: t("files.act.open"), onSelect: () => openDrive(n) }]}
            onOpen={(n) => openDrive(n)}
            empty={{ title: t("drive.empty.shared"), description: t("shared.empty"), className: "empty" }}
          />
        </div>
      )}

      {hasMore && <div ref={sentinel} aria-hidden="true" />}
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
