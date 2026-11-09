// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button } from "antd";
import type { InfiniteData, UseInfiniteQueryResult } from "@tanstack/react-query";
import { useLang } from "../../lib/i18n";
import { useCT } from "../../lib/comments/i18n";
import type { Page, Viewer } from "../../lib/comments/api";
import { formatWhen, viewerName } from "../../lib/comments/logic";
import { faultMessage } from "./faults";
import EmptyState from "../EmptyState";

export type ViewersQuery = UseInfiniteQueryResult<InfiniteData<Page<Viewer>>, Error>;

// ViewersList is the who-viewed list of one item, for a manager, while the account's switch is on (decision 51). It always
// carries the note that it is a convenience and not evidence. A row shows a name while the person reaches the item, the
// former-member text when the person no longer does, and a neutral text when the server did not check the person (past
// its bound). It never shows an ID.
export default function ViewersList({ q }: { q: ViewersQuery }) {
  const { t } = useCT();
  const lang = useLang();
  const items = (q.data?.pages ?? []).flatMap((p) => p.items);

  return (
    <div style={{ display: "grid", gap: 8 }}>
      <p role="note" style={{ margin: 0, fontSize: 13, color: "var(--ant-color-text-secondary)" }}>
        {t("viewers.note")}
      </p>

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
          message={faultMessage(q.error, t, "viewers.failed")}
          action={
            <Button size="small" onClick={() => void q.refetch()}>
              {t("comments.retry")}
            </Button>
          }
        />
      )}

      {q.isError && items.length > 0 && (
        <Alert type="warning" showIcon role="alert" message={t("comments.refreshFailed")} />
      )}

      {q.isSuccess && items.length === 0 && (
        <EmptyState size="compact" text={t("viewers.empty")} />
      )}

      {items.length > 0 && (
        <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 8 }} aria-label={t("viewers.tab")}>
          {items.map((v, i) => {
            const who = viewerName(v);
            return (
              <li key={`${v.user_id}-${i}`} data-testid="viewer" data-state={who.state} style={{ display: "grid", gap: 2 }}>
                <span>
                  {who.state === "named" && <bdi>{who.name}</bdi>}
                  {who.state === "gone" && t("comments.former")}
                  {who.state === "hidden" && t("viewers.hidden")}
                </span>
                <time dateTime={v.viewed_at} style={{ fontSize: 12, color: "var(--ant-color-text-secondary)" }}>
                  {t("viewers.viewedAt", { time: formatWhen(v.viewed_at, lang) })}
                </time>
              </li>
            );
          })}
        </ul>
      )}

      {q.hasNextPage && (
        <div>
          <Button onClick={() => void q.fetchNextPage()} loading={q.isFetchingNextPage}>
            {t("viewers.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}
