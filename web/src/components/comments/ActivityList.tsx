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
import { faultMessage } from "./faults";

// ActivityList is the history of one item, from the activity route (decision 50). The sentences are the audit ones of
// the main catalogues, so a change reads the same here as on the audit page. A page can come back empty with a cursor;
// followEmpty reads on through such pages, so the list never shows "nothing" while more is there.
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

      {q.isSuccess && items.length === 0 && !q.hasNextPage && (
        <p style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>{t("activity.empty")}</p>
      )}

      {items.length > 0 && (
        <ol style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 10 }} aria-label={t("activity.tab")}>
          {items.map((e) => {
            const text = activitySentence(e, youWord, formerWord);
            return (
              <li key={e.id} data-testid="activity-entry" style={{ display: "grid", gap: 2 }}>
                <time dateTime={e.time} style={{ fontSize: 12, color: "var(--ant-color-text-secondary)" }}>
                  {formatWhen(e.time, lang)}
                </time>
                <span>{renderSentence(text.sentence, (key, params) => translate(lang, key, params))}</span>
              </li>
            );
          })}
        </ol>
      )}

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
