// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Dropdown, Input } from "antd";
import { useInfiniteQuery, useQueries, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { useStore } from "../../lib/store";
import { go } from "../../lib/router";
import { isAdmin, session, sessionChanged } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { formatDate } from "../../lib/files/format";
import { debounce, SEARCH_DELAY } from "../../lib/sharing/combobox";
import { listAccounts, updateAccount, usage } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { tenantQuery, tenantSearch, type TenantStatus } from "../../lib/manage/admin";
import type { Account, PersonReply } from "../../lib/manage/types";
import NotFoundPage from "../NotFoundPage";
import Confirm from "../../components/Confirm";
import DeleteTenant from "../../components/admin/DeleteTenant";
import NewTenant from "../../components/admin/NewTenant";
import ShowOnce from "../../components/admin/ShowOnce";
import StatusTag from "../../components/admin/StatusTag";
import UsageMeter from "../../components/UsageMeter";
import { list, more, muted, pageTitle, row, rowMain, rowTitle, toolbar } from "../../components/admin/adminStyles";
import { useGuard, useOnSessionChange } from "../../components/admin/hooks";

// TenantsPage lists every tenant on this installation, for an installation admin only. The search text and the status
// filter are in the address, so a search can be bookmarked. Names are searched on the server; the list is read a page
// at a time.
export default function TenantsPage() {
  const user = useStore(session)?.user;
  return isAdmin(user) ? <TenantsView /> : <NotFoundPage />;
}

const STATUSES: TenantStatus[] = ["all", "active", "suspended"];

function TenantsView() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const qc = useQueryClient();
  const flight = useGuard();
  // The address is read where it is: the router's location store lags behind a replaced address.
  const initial = tenantQuery(window.location.search);
  const [text, setText] = useState(initial.q);
  const [query, setQuery] = useState(initial.q);
  const [status, setStatus] = useState<TenantStatus>(initial.status);
  const [notice, setNotice] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [suspending, setSuspending] = useState<Account | null>(null);
  const [suspendBusy, setSuspendBusy] = useState(false);
  const [suspendError, setSuspendError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Account | null>(null);
  const [secret, setSecret] = useState<{ url: string; name: string } | null>(null);
  const tenantsKey = queryKey("admin-tenants");

  const commit = useMemo(() => debounce((v: string) => setQuery(v.trim().slice(0, 200)), SEARCH_DELAY), []);
  useEffect(() => () => commit.cancel(), [commit]);
  // The form and the secret belong to the person who opened them: a change of person clears both.
  useOnSessionChange(() => {
    setSecret(null);
    setCreating(false);
  });

  // The address follows the search and the filter, so the list can be bookmarked.
  useEffect(() => {
    const next = `/admin/tenants${tenantSearch(query, status)}`;
    if (`${window.location.pathname}${window.location.search}` !== next) go(next, { replace: true });
  }, [query, status]);

  useEffect(() => {
    document.title = `${t("admin.tenants.title")} · ${brandName}`;
  }, [t, brandName]);

  const q = useInfiniteQuery({
    queryKey: queryKey("admin-tenants", query, status),
    queryFn: ({ pageParam }) =>
      listAccounts({ q: query || undefined, status: status === "all" ? undefined : status, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchOnMount: "always",
  });
  const accounts: Account[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const firstFailed = q.isError && q.data === undefined;

  // Each row's usage is read as the row appears. A row whose usage cannot be read shows no meter.
  const usages = useQueries({
    queries: accounts.map((a) => ({
      queryKey: queryKey("usage", a.id),
      queryFn: () => usage(a.id),
      retry: false,
    })),
  });

  const refreshList = () => qc.invalidateQueries({ queryKey: tenantsKey });

  const confirmSuspend = () => {
    const target = suspending;
    if (!target) return Promise.resolve();
    const next = target.status === "suspended" ? "active" : "suspended";
    return flight(async () => {
      const changed = sessionChanged();
      setSuspendBusy(true);
      setSuspendError(null);
      try {
        const updated = await updateAccount(target.id, { status: next });
        if (changed()) return;
        setNotice(
          next === "suspended"
            ? t("tenants.suspendedNotice", { name: updated.name })
            : t("tenants.resumedNotice", { name: updated.name }),
        );
        setSuspending(null);
        void refreshList();
      } catch (err) {
        if (changed()) return;
        setSuspendError(failureText(err, lang));
      } finally {
        setSuspendBusy(false);
      }
    });
  };

  const created = (account: Account, owner: PersonReply | undefined) => {
    setCreating(false);
    void refreshList();
    if (owner?.verification?.url) {
      setSecret({ url: owner.verification.url, name: account.name });
      return;
    }
    go(`/admin/tenants/${account.id}`);
  };

  const menuFor = (a: Account) => ({
    items: [
      {
        // A real link: a middle click or a modified click opens the tenant in a new tab, as the browser does for a link.
        key: "open",
        label: (
          <a
            href={`/admin/tenants/${a.id}`}
            onClick={(e) => {
              if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
              e.preventDefault();
              go(`/admin/tenants/${a.id}`);
            }}
          >
            {t("tenants.open")}
          </a>
        ),
      },
      {
        key: "suspend",
        label: a.status === "suspended" ? t("tenant.resume") : t("tenant.suspend"),
        onClick: () => {
          setSuspendError(null);
          setSuspending(a);
        },
      },
      { key: "delete", danger: true, label: t("tenant.delete.open"), onClick: () => setDeleting(a) },
    ],
  });

  const emptyText = query || status !== "all" ? t("tenants.noMatch") : t("tenants.empty");

  return (
    <div>
      <div style={toolbar}>
        <h1 style={{ ...pageTitle, margin: 0 }}>{t("admin.tenants.title")}</h1>
        <Button type="primary" onClick={() => setCreating(true)}>
          {t("tenant.new.open")}
        </Button>
      </div>

      <div style={{ display: "grid", gap: 8, marginBlockEnd: 16 }}>
        <label htmlFor="tenant-search" style={{ fontWeight: 500 }}>
          {t("tenants.search")}
        </label>
        <Input
          id="tenant-search"
          type="search"
          autoComplete="off"
          value={text}
          placeholder={t("tenants.searchHint")}
          onChange={(e) => {
            setText(e.target.value);
            commit.call(e.target.value);
          }}
        />
        <div role="group" aria-label={t("tenants.filter")} style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
          {STATUSES.map((s) => (
            <Button key={s} aria-pressed={status === s} type={status === s ? "primary" : "default"} onClick={() => setStatus(s)}>
              {t(`tenants.${s}`)}
            </Button>
          ))}
        </div>
      </div>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBlockEnd: 12 }} />}
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}

      {!firstFailed && q.isPending && <p role="status">{t("app.loading")}</p>}
      {q.isSuccess && accounts.length === 0 && (
        <p role="status" style={muted}>
          {emptyText}
        </p>
      )}
      {!firstFailed && accounts.length > 0 && (
        <ul style={list} aria-label={t("admin.tenants.title")}>
          {accounts.map((a, i) => {
            const u = usages[i]?.data;
            return (
              <li key={a.id} style={row}>
                <div style={rowMain}>
                  <p style={rowTitle}>
                    <a href={`/admin/tenants/${a.id}`}>
                      <bdi>{a.name}</bdi>
                    </a>
                  </p>
                  {a.external_id && (
                    <p style={{ margin: 0, fontFamily: "ui-monospace, monospace", overflowWrap: "anywhere" }}>
                      <bdi>{a.external_id}</bdi>
                    </p>
                  )}
                </div>
                <div>
                  <StatusTag tone={a.status === "active" ? "ok" : a.status === "suspended" ? "danger" : "neutral"}>
                    {t(`tenant.status.${a.status}`)}
                  </StatusTag>
                </div>
                <div style={{ flex: "2 1 220px", minWidth: 180 }}>
                  {u ? (
                    <UsageMeter used={u.used_bytes} quota={u.quota_bytes} />
                  ) : usages[i]?.isError ? (
                    <span style={muted}>{t("usage.failed")}</span>
                  ) : (
                    <span style={muted}>{t("usage.loading")}</span>
                  )}
                </div>
                <div>
                  <time dateTime={a.created_at} title={formatDate(a.created_at, lang)}>
                    {formatDate(a.created_at, lang)}
                  </time>
                </div>
                <Dropdown menu={menuFor(a)} trigger={["click"]}>
                  <Button size="small" aria-haspopup="menu" aria-label={t("tenants.actionsFor", { name: a.name })}>
                    …
                  </Button>
                </Dropdown>
              </li>
            );
          })}
        </ul>
      )}
      {!firstFailed && q.hasNextPage && (
        <div style={more}>
          <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}

      {creating && <NewTenant onClose={() => setCreating(false)} onCreated={created} />}

      {secret !== null && (
        <ShowOnce
          id="tenant-link"
          title={t("tenant.new.linkTitle", { name: secret.name })}
          label={t("tenant.new.linkLabel")}
          value={secret.url}
          warning={t("tenant.new.linkWarning")}
          onClosed={() => setSecret(null)}
        />
      )}

      {suspending !== null && (
        <Confirm
          id="suspend-tenant"
          title={
            suspending.status === "suspended"
              ? t("tenant.resumeTitle", { name: suspending.name })
              : t("tenant.suspendTitle", { name: suspending.name })
          }
          message={
            suspending.status === "suspended" ? t("tenant.resumeMessage") : t("tenant.suspendMessage", { name: suspending.name })
          }
          action={suspending.status === "suspended" ? t("tenant.resume") : t("tenant.suspend")}
          error={suspendError}
          busy={suspendBusy}
          onconfirm={() => void confirmSuspend()}
          onclose={() => setSuspending(null)}
        />
      )}

      {deleting !== null && (
        <DeleteTenant
          id={deleting.id}
          name={deleting.name}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            const gone = deleting.name;
            setDeleting(null);
            setNotice(t("tenants.deletedNotice", { name: gone }));
            void refreshList();
          }}
        />
      )}
    </div>
  );
}
