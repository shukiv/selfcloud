// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Input } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { ApiError } from "../../lib/apiError";
import { useStore } from "../../lib/store";
import { go } from "../../lib/router";
import { isAdmin, session, sessionChanged } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { getAccount, updateAccount } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { nextTab, TENANT_TABS, tabFrom, type TenantTab } from "../../lib/manage/admin";
import type { Account } from "../../lib/manage/types";
import NotFoundPage from "../NotFoundPage";
import Confirm from "../../components/Confirm";
import DeleteTenant from "../../components/admin/DeleteTenant";
import StatusTag from "../../components/admin/StatusTag";
import TenantAudit from "../../components/admin/TenantAudit";
import TenantKeys from "../../components/admin/TenantKeys";
import TenantOverview from "../../components/admin/TenantOverview";
import TenantUsers from "../../components/admin/TenantUsers";
import { muted, pageTitle } from "../../components/admin/adminStyles";
import { useGuard } from "../../components/admin/hooks";

// TenantPage shows one tenant to an installation admin. The tabs are in the address (?tab=), so a tab can be bookmarked.
// The admin renames, suspends, resumes and deletes the tenant here; members are the owner's to manage.
export default function TenantPage({ account }: { account: string }) {
  const user = useStore(session)?.user;
  return isAdmin(user) ? <TenantView id={account} /> : <NotFoundPage />;
}

function TenantView({ id }: { id: string }) {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const qc = useQueryClient();
  const flight = useGuard();
  const tenantKey = queryKey("admin-tenant", id);
  const q = useQuery({ queryKey: tenantKey, queryFn: () => getAccount(id), refetchOnMount: "always", retry: false });
  const tenant = q.data;
  const [tab, setTab] = useState<TenantTab>(() => tabFrom(window.location.search));
  const [notice, setNotice] = useState<string | null>(null);
  const [renaming, setRenaming] = useState(false);
  const [newName, setNewName] = useState("");
  const [renameBusy, setRenameBusy] = useState(false);
  const [renameError, setRenameError] = useState<string | null>(null);
  const [suspending, setSuspending] = useState(false);
  const [suspendBusy, setSuspendBusy] = useState(false);
  const [suspendError, setSuspendError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const tabEls = useRef<Array<HTMLButtonElement | null>>([]);

  useEffect(() => {
    document.title = `${tenant?.name ?? t("admin.tenants.title")} · ${brandName}`;
  }, [t, brandName, tenant?.name]);

  const setAccount = (a: Account) => {
    qc.setQueryData(tenantKey, a);
    void qc.invalidateQueries({ queryKey: queryKey("admin-tenants") });
  };

  const selectTab = (next: TenantTab, focus = false) => {
    setTab(next);
    go(`/admin/tenants/${id}?tab=${next}`, { replace: true });
    if (focus) tabEls.current[TENANT_TABS.indexOf(next)]?.focus();
  };

  const tabKeys = (e: KeyboardEvent) => {
    const next = nextTab(tab, e.key);
    if (next === null) return;
    e.preventDefault();
    selectTab(next, true);
  };

  const saveName = (e: FormEvent) => {
    e.preventDefault();
    if (!tenant || newName.trim() === "") return;
    const target = tenant;
    void flight(async () => {
      const changed = sessionChanged();
      setRenameBusy(true);
      setRenameError(null);
      try {
        const updated = await updateAccount(target.id, { name: newName.trim() });
        if (changed()) return;
        setAccount(updated);
        setRenaming(false);
        setNotice(t("tenant.renamed"));
      } catch (err) {
        if (changed()) return;
        setRenameError(failureText(err, lang));
      } finally {
        setRenameBusy(false);
      }
    });
  };

  const confirmSuspend = () => {
    if (!tenant) return Promise.resolve();
    const target = tenant;
    const next = target.status === "suspended" ? "active" : "suspended";
    return flight(async () => {
      const changed = sessionChanged();
      setSuspendBusy(true);
      setSuspendError(null);
      try {
        const updated = await updateAccount(target.id, { status: next });
        if (changed()) return;
        setAccount(updated);
        setSuspending(false);
        setNotice(next === "suspended" ? t("tenant.suspendedNotice") : t("tenant.resumedNotice"));
      } catch (err) {
        if (changed()) return;
        setSuspendError(failureText(err, lang));
      } finally {
        setSuspendBusy(false);
      }
    });
  };

  if (q.isError && tenant === undefined) {
    const notFound = q.error instanceof ApiError && q.error.status === 404;
    return (
      <div>
        <Alert type="error" showIcon role="alert" message={notFound ? t("tenant.notFound") : failureText(q.error, lang)} />
        <p>
          <a href="/admin/tenants">{t("tenant.back")}</a>
        </p>
      </div>
    );
  }
  if (!tenant) return <p role="status">{t("app.loading")}</p>;
  // A failed refetch keeps the tenant on screen, but its status may be stale, and Suspend or Resume depends on it: the
  // actions wait until a read succeeds.
  const stale = q.isError;

  return (
    <div>
      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBlockEnd: 12 }} />}
      {stale && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}
      <p style={{ margin: "0 0 8px", fontSize: 14 }}>
        <a href="/admin/tenants">{t("admin.tenants.title")}</a>
      </p>

      <header style={{ display: "grid", gap: 12, marginBlockEnd: 16 }}>
        {renaming ? (
          <form style={{ display: "flex", flexWrap: "wrap", gap: 8 }} onSubmit={saveName} noValidate>
            <label htmlFor="tenant-name" className="sr-only">
              {t("tenant.nameLabel")}
            </label>
            <Input
              id="tenant-name"
              maxLength={200}
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              autoFocus
              style={{ flex: "1 1 220px", minWidth: 0 }}
            />
            <Button type="primary" htmlType="submit" loading={renameBusy} disabled={renameBusy || newName.trim() === ""}>
              {t("tenant.save")}
            </Button>
            <Button onClick={() => setRenaming(false)}>{t("common.cancel")}</Button>
            {renameError !== null && <Alert type="error" showIcon role="alert" message={renameError} style={{ flexBasis: "100%" }} />}
          </form>
        ) : (
          <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 12 }}>
            <h1 style={{ ...pageTitle, margin: 0, overflowWrap: "anywhere" }}>
              <bdi>{tenant.name}</bdi>
            </h1>
            <Button
              size="small"
              onClick={() => {
                setNewName(tenant.name);
                setRenameError(null);
                setRenaming(true);
              }}
            >
              {t("tenant.rename")}
            </Button>
          </div>
        )}
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
          <StatusTag tone={tenant.status === "active" ? "ok" : tenant.status === "suspended" ? "danger" : "neutral"}>
            {t(`tenant.status.${tenant.status}`)}
          </StatusTag>
          {tenant.external_id && (
            <span style={{ fontFamily: "ui-monospace, monospace", ...muted }}>
              {t("tenant.externalId")}: <bdi>{tenant.external_id}</bdi>
            </span>
          )}
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
          <Button
            disabled={stale}
            onClick={() => {
              setSuspendError(null);
              setSuspending(true);
            }}
          >
            {tenant.status === "suspended" ? t("tenant.resume") : t("tenant.suspend")}
          </Button>
          <Button danger disabled={stale} onClick={() => setDeleting(true)}>
            {t("tenant.delete.open")}
          </Button>
        </div>
      </header>

      <div role="tablist" aria-label={t("tenant.tabs")} style={{ display: "flex", flexWrap: "wrap", gap: 8, marginBlockEnd: 16 }}>
        {TENANT_TABS.map((name, i) => (
          <Button
            key={name}
            ref={(el: HTMLButtonElement | null) => {
              tabEls.current[i] = el;
            }}
            role="tab"
            id={`tab-${name}`}
            aria-selected={tab === name}
            aria-controls={`panel-${name}`}
            tabIndex={tab === name ? 0 : -1}
            type={tab === name ? "primary" : "default"}
            onClick={() => selectTab(name)}
            onKeyDown={tabKeys}
          >
            {t(`tenant.tab.${name}`)}
          </Button>
        ))}
      </div>

      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {tab === "overview" && <TenantOverview account={tenant} onChanged={setAccount} />}
        {tab === "users" && <TenantUsers account={tenant.id} />}
        {tab === "keys" && <TenantKeys account={tenant.id} />}
        {tab === "audit" && <TenantAudit account={tenant.id} />}
      </div>

      {suspending && (
        <Confirm
          id="suspend-tenant-page"
          title={
            tenant.status === "suspended" ? t("tenant.resumeTitle", { name: tenant.name }) : t("tenant.suspendTitle", { name: tenant.name })
          }
          message={tenant.status === "suspended" ? t("tenant.resumeMessage") : t("tenant.suspendMessage", { name: tenant.name })}
          action={tenant.status === "suspended" ? t("tenant.resume") : t("tenant.suspend")}
          error={suspendError}
          busy={suspendBusy}
          onconfirm={() => void confirmSuspend()}
          onclose={() => setSuspending(false)}
        />
      )}

      {deleting && (
        <DeleteTenant id={tenant.id} name={tenant.name} onClose={() => setDeleting(false)} onDeleted={() => go("/admin/tenants")} />
      )}
    </div>
  );
}
