// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button } from "antd";
import { useInfiniteQuery } from "@tanstack/react-query";
import { renderSentence } from "../../lib/audit/sentence";
import { translate, useLang } from "../../lib/i18n";
import { followEmpty } from "../../lib/comments/logic";
import { listActivity, type ActivityEntry, type Page } from "../../lib/comments/api";
import { activitySentence, formatWhen, nextPage } from "../../lib/comments/logic";
import { useCT } from "../../lib/comments/i18n";
import { queryKey } from "../../lib/query";
import { useLoadMoreSentinel } from "../../lib/files/loadMoreSentinel";
import Listing, { type ListColumn } from "../files/Listing";
import { faultMessage } from "./faults";

// ActivityList is the history of one item, from the activity route (decision 50), on one table (tl5, decision 138). The sentences
// are the audit ones of the main catalogues, so a change reads the same here as on the audit page. The panel is narrow, so the
// phone layout is always used: the event is the line under the time. A page can come back empty with a cursor; followEmpty reads
// on through such pages, so the list never shows "nothing" while more is there. The entries are of this item, so a row opens
// nothing.
export default function ActivityList({ nodeID }: { nodeID: string }) {
  const { t } = useCT();
  const lang = useLang();
  const q = useInfiniteQuery({
    queryKey: queryKey("activity", nodeID),
    queryFn: ({ pageParam }) => followEmpty<ActivityEntry>((c) => listActivity(nodeID, c), pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<ActivityEntry>) => nextPage(last),
    refetchOnMount: "always",
  });
  const items = (q.data?.pages ?? []).flatMap((p) => p.items);
  const youWord = t("comments.mine");
  const formerWord = t("comments.former");
  const eventText = (e: ActivityEntry) => renderSentence(activitySentence(e, youWord, formerWord).sentence, (key, params) => translate(lang, key, params));
  const sentinel = useLoadMoreSentinel(q.hasNextPage, q.isFetchingNextPage, q.isFetchNextPageError, () => void q.fetchNextPage());

  const columns: ListColumn<ActivityEntry>[] = [
    {
      key: "when",
      title: t("activity.colWhen"),
      ariaSort: "descending",
      render: (e) => (
        <time dateTime={e.time} style={{ fontSize: 12, color: "var(--ant-color-text-secondary)" }}>
          {formatWhen(e.time, lang)}
        </time>
      ),
    },
    {
      key: "event",
      title: t("activity.colEvent"),
      secondary: true,
      render: eventText,
    },
  ];

  return (
    <div style={{ display: "grid", gap: 4 }}>
      {q.isPending && (
        <p role="status" style={{ margin: 0, fontSize: 13 }}>
          {t("comments.loading")}
        </p>
      )}

      {q.isError && items.length === 0 && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={faultMessage(q.error, t, "activity.failed")}
          action={
            <Button size="small" onClick={() => void q.refetch()}>
              {t("comments.retry")}
            </Button>
          }
        />
      )}

      {q.isError && items.length > 0 && (
        <Alert type="warning" showIcon role="alert" message={t("comments.refreshFailed")} style={{ marginBlockEnd: 8 }} />
      )}

      {(items.length > 0 || (q.isSuccess && !q.hasNextPage)) && (
        <Listing<ActivityEntry>
          label={t("activity.tab")}
          items={items}
          rowLabel={(e) => `${formatWhen(e.time, lang)}: ${eventText(e)}`}
          rowTestID={() => "activity-entry"}
          columns={columns}
          phone
          stickyTop={0}
          empty={{ title: t("activity.emptyTitle"), description: t("activity.empty") }}
        />
      )}

      {q.hasNextPage && <div ref={sentinel} aria-hidden="true" />}

      {q.hasNextPage && (
        <div>
          <Button onClick={() => void q.fetchNextPage()} loading={q.isFetchingNextPage}>
            {t("activity.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}
