// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Space } from "antd";
import type { InfiniteData, UseInfiniteQueryResult } from "@tanstack/react-query";
import { useCT } from "../../lib/comments/i18n";
import type { Comment, Page } from "../../lib/comments/api";
import { canComment } from "../../lib/comments/logic";
import Composer from "./Composer";
import CommentItem from "./CommentItem";
import { faultMessage } from "./faults";
import EmptyState from "../EmptyState";

export type CommentsQuery = UseInfiniteQueryResult<InfiniteData<Page<Comment>>, Error>;

// CommentsPanel lists the top-level comments of an item, newest first, with the composer for a commenter. The list is
// the query's: a failed refetch keeps the list on the screen, and says so; a failed first load shows an error with a
// try-again button. The query is made by the tabs, which also read the comments switch from it.
export default function CommentsPanel({
  nodeID,
  role,
  q,
}: {
  nodeID: string;
  role: string | null | undefined;
  q: CommentsQuery;
}) {
  const { t } = useCT();
  const items = (q.data?.pages ?? []).flatMap((p) => p.items);
  const writable = canComment(role);

  return (
    <div style={{ display: "grid", gap: 4 }}>
      {writable && (
        <Composer nodeID={nodeID} label={t("comments.newLabel")} submit={t("comments.post")} />
      )}

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
          message={faultMessage(q.error, t, "comments.failed")}
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

      {q.isSuccess && items.length === 0 && (
        <EmptyState size="compact" text={t("comments.empty")} />
      )}

      {items.length > 0 && (
        <ul style={{ listStyle: "none", margin: 0, padding: 0 }} aria-label={t("comments.tab")}>
          {items.map((c) => (
            <li key={c.id}>
              <CommentItem nodeID={nodeID} comment={c} role={role} />
            </li>
          ))}
        </ul>
      )}

      {q.hasNextPage && (
        <Space>
          <Button onClick={() => void q.fetchNextPage()} loading={q.isFetchingNextPage}>
            {t("comments.loadMore")}
          </Button>
        </Space>
      )}
    </div>
  );
}
