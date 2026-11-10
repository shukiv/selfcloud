// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, App, Button, Switch, theme } from "antd";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import {
  getEmailSwitch,
  listNotices,
  markRead,
  setEmailSwitch,
  type Notice,
  type NoticePage,
} from "../lib/notifications/api";
import { detailsPath, followEmpty, formatWhen, nextPage, noticeSentence, validNotice } from "../lib/notifications/logic";
import { useNT } from "../lib/notifications/i18n";
import { useLang, useT } from "../lib/i18n";
import { session } from "../lib/session";
import { useStore } from "../lib/store";
import { queryKey } from "../lib/query";
import { useCoarsePointer } from "../lib/pointer";
import { RADIUS } from "../theme/tokens";
import Listing, { type ListColumn } from "../components/files/Listing";
import { useLoadMoreSentinel } from "../lib/files/loadMoreSentinel";

// NotificationsPage is the signed-in person's inbox, newest first, one page at a time, with the switch for email beside it.
// The notices are one table (tl5, decision 138): the message is the name cell, its button opens the notice; the status is a
// second line on a phone; When is marked newest first. Opening a row or its button marks it read and goes to the file with its
// comments shown. A failed refresh keeps the list on the screen; a failed next page says so, and is not asked again by itself
// (Show more asks); a failed first load says so too. While the person is signed in as another (an administrator's impersonation),
// the inbox is not read at all, and the page says so.
export default function NotificationsPage() {
  const { t: mt } = useT();
  const { t, ready, failed } = useNT();
  const lang = useLang();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { message } = App.useApp();
  const coarse = useCoarsePointer();
  const me = useStore(session);
  const blocked = !!me?.impersonation;
  const listKey = queryKey("notifications", "list");
  const emailKey = queryKey("notifications", "settings");

  const list = useInfiniteQuery({
    queryKey: listKey,
    // A page can come back empty when the person no longer reaches its items; followEmpty reads on through such pages.
    queryFn: ({ pageParam }) => followEmpty<Notice>((c) => listNotices(c), pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: NoticePage) => nextPage(last),
    refetchOnMount: "always",
    enabled: !blocked,
  });
  const items = (list.data?.pages ?? []).flatMap((p) => p.items);

  const email = useQuery({ queryKey: emailKey, queryFn: getEmailSwitch, retry: false, enabled: !blocked });
  const save = useMutation({
    mutationFn: (on: boolean) => setEmailSwitch(on),
    onSuccess: (data) => qc.setQueryData(emailKey, data),
    // The switch shows the saved value, so a refused save leaves it where it was.
    onError: () => message.error(t("notifications.saveFailed")),
  });

  // markOne marks a line read, and shows it read in the list at once. The bell's count is read again after it.
  async function markOne(n: Notice) {
    try {
      await markRead(n.id);
      qc.setQueryData<InfiniteData<NoticePage, string | undefined>>(listKey, (old) =>
        old && {
          ...old,
          pages: old.pages.map((p) => ({
            ...p,
            items: p.items.map((x) => (x.id === n.id ? { ...x, read_at: new Date().toISOString() } : x)),
          })),
        },
      );
      void qc.invalidateQueries({ queryKey: queryKey("notifications", "unread") });
    } catch {
      message.error(t("notifications.markFailed"));
    }
  }

  // open marks a line read and goes to its file, with the comments shown. The address is made from the node's ID only
  // when that ID has the server's shape; a line whose ID does not is not opened.
  function open(n: Notice) {
    if (!validNotice(n)) {
      message.error(t("notifications.badLink"));
      return;
    }
    if (!n.read_at) void markOne(n);
    navigate(detailsPath(n.node_id));
  }

  // A touch target is 44 px on a finger, and the usual size with a mouse.
  const touch = coarse ? { minHeight: 44 } : undefined;
  const { token } = theme.useToken();
  // The next page is asked for when the end of the list comes into view, once. A failed next page is not asked for again by
  // itself: the message says Show more asks again.
  const sentinel = useLoadMoreSentinel(!!list.hasNextPage && !list.isFetchNextPageError, list.isFetchingNextPage, () => void list.fetchNextPage());

  const columns: ListColumn<Notice>[] = [
    {
      key: "message",
      title: t("notifications.colMessage"),
      render: (n) => (
        // The sentence takes the page's direction: a bdi would take the direction of its first Latin letter, and a Hebrew line that
        // starts with a name would then read in the wrong order.
        <button
          type="button"
          onClick={() => open(n)}
          style={{
            display: "block",
            maxWidth: "100%",
            minHeight: coarse ? 44 : undefined,
            padding: "8px 0",
            textAlign: "start",
            background: "none",
            border: 0,
            font: "inherit",
            color: "inherit",
            cursor: "pointer",
            fontWeight: n.read_at ? 400 : 700,
            overflowWrap: "anywhere",
          }}
        >
          {noticeSentence(n, t)}
        </button>
      ),
    },
    {
      key: "status",
      title: t("notifications.colStatus"),
      secondary: true,
      render: (n) => (
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6, fontWeight: n.read_at ? 400 : 600 }}>
          <span
            aria-hidden="true"
            style={{
              width: 8,
              height: 8,
              borderRadius: "50%",
              flex: "none",
              background: n.read_at ? "transparent" : token.colorPrimary,
              border: n.read_at ? `1px solid ${token.colorBorder}` : "none",
            }}
          />
          {n.read_at ? t("notifications.read") : t("notifications.unread")}
        </span>
      ),
    },
    {
      key: "when",
      title: t("notifications.colWhen"),
      ariaSort: "descending",
      width: 170,
      render: (n) => (
        <time dateTime={n.created_at} style={{ fontSize: token.fontSizeSM }}>
          {formatWhen(n.created_at, lang)}
        </time>
      ),
    },
  ];

  if (!ready) {
    // The catalogue did not load: the main catalogue says so, and a reload asks again.
    if (failed) {
      return (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={mt("app.crash.title")}
          action={
            <Button onClick={() => window.location.reload()} style={touch}>
              {mt("app.crash.reload")}
            </Button>
          }
        />
      );
    }
    return null;
  }

  return (
    <div style={{ display: "grid", gap: 20, maxWidth: 760 }}>
      <h1 style={{ fontSize: 22, fontWeight: 700, margin: 0 }}>{t("notifications.title")}</h1>

      {blocked && <Alert type="info" showIcon role="status" message={t("notifications.impersonating")} />}

      {!blocked && (
        <section
          aria-labelledby="notify-email-title"
          style={{ border: "1px solid var(--ant-color-border-secondary)", borderRadius: RADIUS, padding: 16, display: "grid", gap: 8 }}
        >
          <h2 id="notify-email-title" style={{ fontSize: 16, fontWeight: 600, margin: 0 }}>
            {t("notifications.emailTitle")}
          </h2>
          {email.isError && !email.data ? (
            <Alert
              type="error"
              showIcon
              role="alert"
              message={t("notifications.emailLoadFailed")}
              action={
                <Button onClick={() => void email.refetch()} loading={email.isFetching} style={touch}>
                  {t("notifications.retry")}
                </Button>
              }
            />
          ) : (
            <label style={{ display: "flex", alignItems: "center", gap: 12, minHeight: coarse ? 44 : undefined, cursor: "pointer" }}>
              <Switch
                checked={save.isPending ? save.variables : (email.data?.email ?? false)}
                disabled={!email.data || save.isPending}
                checkedChildren={t("notifications.on")}
                unCheckedChildren={t("notifications.off")}
                onChange={(on) => save.mutate(on)}
              />
              <span>{t("notifications.emailLabel")}</span>
            </label>
          )}
          <p style={{ margin: 0, fontSize: 13, color: "var(--ant-color-text-secondary)" }}>{t("notifications.emailHelp")}</p>
        </section>
      )}

      {!blocked && list.isPending && (
        <p role="status" style={{ margin: 0 }}>
          {t("notifications.loading")}
        </p>
      )}

      {!blocked && list.isError && items.length === 0 && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={t("notifications.failed")}
          action={
            <Button onClick={() => void list.refetch()} style={touch}>
              {t("notifications.retry")}
            </Button>
          }
        />
      )}

      {/* A failed next page is said as such: the list on the screen is fine, and "show more" asks again. The refresh
          message is for a failed read of the first pages. */}
      {!blocked && list.isFetchNextPageError && (
        <Alert type="warning" showIcon role="alert" message={t("notifications.moreFailed")} />
      )}

      {!blocked && list.isError && !list.isFetchNextPageError && items.length > 0 && (
        <Alert type="warning" showIcon role="alert" message={t("notifications.refreshFailed")} />
      )}

      {!blocked && (items.length > 0 || (list.isSuccess && !list.hasNextPage)) && (
        <Listing<Notice>
          label={t("notifications.title")}
          items={items}
          rowLabel={(n) => noticeSentence(n, t)}
          rowTestID={() => "notice"}
          columns={columns}
          onOpen={(n) => open(n)}
          empty={{ title: t("notifications.emptyTitle"), description: t("notifications.empty") }}
        />
      )}

      {!blocked && list.hasNextPage && <div ref={sentinel} aria-hidden="true" />}

      {!blocked && list.hasNextPage && (
        <div>
          <Button onClick={() => void list.fetchNextPage()} loading={list.isFetchingNextPage} style={touch}>
            {t("notifications.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}
