// SPDX-License-Identifier: AGPL-3.0-or-later

import { App, Alert, Button, Input } from "antd";
import { useFlight } from "../../lib/flight";
import { keepPreviousData, useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { useStore } from "../../lib/store";
import { go } from "../../lib/router";
import { isAdmin, session, sessionChanged } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { formatDate } from "../../lib/files/format";
import { debounce, SEARCH_DELAY } from "../../lib/sharing/combobox";
import { listAccounts, updateAccount } from "../../lib/manage/api";
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
import Listing, { type ListColumn, type ListMenuEntry, type ListSort } from "../../components/files/Listing";
import { more, pageTitle, toolbar } from "../../components/admin/adminStyles";
import { useGuard, useOnSessionChange } from "../../components/admin/hooks";
import { useAdminText } from "../../lib/manage/adminText";

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
  // The suspend or resume on its way is the request the dialog cancels (fu4, decision 76's rule for writes).
  const request = useFlight();
  const { message } = App.useApp();
  const cancelSuspend = () => {
    if (request.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      void refreshList();
    }
    setSuspendError(null);
    setSuspending(null);
  };
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

  // The list is sorted on the server by name or by creation time (decision A1). Name, ascending, is the default.
  const [sort, setSort] = useState<{ key: "name" | "created"; dir: "asc" | "desc" }>({ key: "name", dir: "asc" });
  const q = useInfiniteQuery({
    queryKey: queryKey("admin-tenants", query, status, sort.key, sort.dir),
    queryFn: ({ pageParam }) =>
      listAccounts({
        q: query || undefined,
        status: status === "all" ? undefined : status,
        sort: sort.key,
        order: sort.dir,
        cursor: pageParam,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchOnMount: "always",
    // A new sort or search keeps the list on screen until its first page is in, so the table does not vanish and come back.
    placeholderData: keepPreviousData,
  });
  const accounts: Account[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const firstFailed = q.isError && q.data === undefined;

  // A header click sorts by its key; a new key starts ascending, and the same key turns the direction over.
  const sortBy = (key: string) => {
    if (key !== "name" && key !== "created") return;
    setSort((s) => (s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: "asc" }));
  };

  const refreshList = () => qc.invalidateQueries({ queryKey: tenantsKey });

  const confirmSuspend = () => {
    const target = suspending;
    if (!target) return Promise.resolve();
    const next = target.status === "suspended" ? "active" : "suspended";
    return flight(async () => {
      const changed = sessionChanged();
      const signal = request.start();
      setSuspendBusy(true);
      setSuspendError(null);
      try {
        const updated = await updateAccount(target.id, { status: next }, signal);
        if (changed()) return;
        setNotice(
          next === "suspended"
            ? t("tenants.suspendedNotice", { name: updated.name })
            : t("tenants.resumedNotice", { name: updated.name }),
        );
        setSuspending(null);
        void refreshList();
      } catch (err) {
        if (changed() || signal.aborted) return;
        setSuspendError(failureText(err, lang));
      } finally {
        request.end();
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

  // The ⋮ menu of a tenant: Open, Suspend (or Resume) and Delete. Open is a link, as the name is, so a middle or modified
  // click opens the tenant in a new tab (review L2).
  const open = (a: Account) => go(`/admin/tenants/${a.id}`);
  const menuFor = (a: Account): ListMenuEntry[] => [
    { key: "open", label: t("tenants.open"), href: `/admin/tenants/${a.id}`, onSelect: () => open(a) },
    {
      key: "suspend",
      label: a.status === "suspended" ? t("tenant.resume") : t("tenant.suspend"),
      onSelect: () => {
        setSuspendError(null);
        setSuspending(a);
      },
    },
    { key: "delete", danger: true, label: t("tenant.delete.open"), onSelect: () => setDeleting(a) },
  ];

  // The columns of the tenants list (design-tl section 3.1, the approved mockup). Name and Created sort on the server; Usage
  // and Users read the account's own used and quota bytes and its user count, so the list makes no request per tenant.
  const columns: ListColumn<Account>[] = [
    {
      key: "name",
      title: t("files.col.name"),
      sortKey: "name",
      render: (a) => (
        <span style={{ minWidth: 0 }}>
          <a href={`/admin/tenants/${a.id}`}>
            <bdi>{a.name}</bdi>
          </a>
          {a.external_id && (
            <span style={{ display: "block", fontFamily: "ui-monospace, monospace", fontSize: 13, overflowWrap: "anywhere", color: "var(--ant-color-text-secondary)" }}>
              <bdi>{a.external_id}</bdi>
            </span>
          )}
        </span>
      ),
    },
    {
      key: "status",
      title: t("people.detail.status"),
      width: 120,
      render: (a) => (
        <StatusTag tone={a.status === "active" ? "ok" : a.status === "suspended" ? "danger" : "neutral"}>{t(`tenant.status.${a.status}`)}</StatusTag>
      ),
    },
    {
      key: "usage",
      title: t("tenant.overview.usage"),
      secondary: true,
      render: (a) => <UsageMeter used={a.used_bytes} quota={a.quota_bytes > 0 ? a.quota_bytes : null} />,
    },
    {
      key: "users",
      title: t("tenant.tab.users"),
      secondary: true,
      align: "end",
      width: 88,
      render: (a) => <span style={{ fontVariantNumeric: "tabular-nums" }}>{a.user_count ?? ""}</span>,
    },
    {
      key: "created",
      title: t("tenant.overview.created"),
      sortKey: "created",
      secondary: true,
      width: 150,
      render: (a) => (
        <time dateTime={a.created_at} title={formatDate(a.created_at, lang)}>
          {formatDate(a.created_at, lang)}
        </time>
      ),
    },
  ];
  const listSort: ListSort = { key: sort.key, dir: sort.dir, onSort: sortBy };

  // A search or a status that matches nothing says so, with no action; an installation with no tenant yet offers New tenant.
  const filtered = query !== "" || status !== "all";
  const adminText = useAdminText();

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
      {!firstFailed && q.data !== undefined && (
        <Listing
          label={t("admin.tenants.title")}
          items={accounts}
          rowLabel={(a) => a.name}
          moreLabel={(a) => t("tenants.actionsFor", { name: a.name })}
          columns={columns}
          menu={menuFor}
          sort={listSort}
          // A row opens its tenant: a click on it, or Enter (design-tl 2.3).
          onOpen={open}
          empty={{
            title: filtered ? t("tenants.noMatch") : t("tenants.empty"),
            description: !filtered && adminText ? t("admin.empty.tenants") : undefined,
            action: filtered ? undefined : { label: t("tenant.new.open"), onClick: () => setCreating(true) },
          }}
        />
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
          oncancel={cancelSuspend}
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
