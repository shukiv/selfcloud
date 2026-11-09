// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, App, Button, Switch } from "antd";
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
import EmptyState from "../components/EmptyState";

// NotificationsPage is the signed-in person's inbox, newest first, one page at a time, with the switch for email beside it.
// Each line says who did what, on which file, in text. Opening a line marks it read and goes to the file with its comments
// shown. A failed refresh keeps the list on the screen; a failed next page says so; a failed first load says so too. While
// the person is signed in as another (an administrator's impersonation), the inbox is not read at all, and the page says so.
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

      {!blocked && list.isSuccess && items.length === 0 && !list.hasNextPage && (
        <EmptyState text={t("notifications.empty")} />
      )}

      {!blocked && items.length > 0 && (
        <ol aria-label={t("notifications.title")} style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 8 }}>
          {items.map((n) => (
            <li key={n.id} data-testid="notice">
              <button
                type="button"
                onClick={() => open(n)}
                style={{
                  display: "grid",
                  gap: 4,
                  width: "100%",
                  minHeight: coarse ? 44 : undefined,
                  textAlign: "start",
                  padding: "10px 12px",
                  border: "1px solid var(--ant-color-border-secondary)",
                  borderRadius: RADIUS,
                  background: "var(--ant-color-bg-container)",
                  color: "var(--ant-color-text)",
                  font: "inherit",
                  cursor: "pointer",
                }}
              >
                {/* The sentence takes the page's direction: a bdi would take the direction of its first Latin letter, and a
                    Hebrew line that starts with a name would then read in the wrong order. */}
                <span style={{ fontWeight: n.read_at ? 400 : 700 }}>{noticeSentence(n, t)}</span>
                <span style={{ display: "flex", gap: 12, flexWrap: "wrap", fontSize: 13, color: "var(--ant-color-text-secondary)" }}>
                  <span>{n.read_at ? t("notifications.read") : t("notifications.unread")}</span>
                  <time dateTime={n.created_at}>{formatWhen(n.created_at, lang)}</time>
                </span>
              </button>
            </li>
          ))}
        </ol>
      )}

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
