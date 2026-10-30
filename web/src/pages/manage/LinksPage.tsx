// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Select } from "antd";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { formatDate } from "../../lib/files/format";
import { getSharingPolicy, deleteShareLink } from "../../lib/sharing/api";
import { failureText } from "../../lib/manage/failure";
import { listAccountLinks, PAGE_SIZE } from "../../lib/manage/api";
import {
  audienceLabel,
  creatorLabel,
  folderLabel,
  itemLabel,
  kindLabel,
  mergeLinks,
  NO_FILTER,
  pathLabel,
  statusKeys,
  type AccountLink,
  type AudienceFilter,
  type KindFilter,
  type Label,
  type LinkFilter,
} from "../../lib/manage/links";
import { queryKey } from "../../lib/query";
import { session, sessionChanged } from "../../lib/session";
import { useStore } from "../../lib/store";
import Confirm from "../../components/Confirm";
import DeletePublicLinks from "../../components/share/DeletePublicLinks";
import { OwnerOnly, useAccountID, useManageTitle, useOnce } from "../../components/manage/OwnerPage";

// Links: every link of the account that anyone can open, and every drop link, with its place and its state. An owner
// deletes one at a time, or all of them at once. Links that only people in the account can open are on the items they
// sit on, and are not listed here.
function LinksView() {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  useManageTitle("links.title");
  const [filter, setFilter] = useState<LinkFilter>({ ...NO_FILTER });
  const [notice, setNotice] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<AccountLink | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const once = useOnce();
  const viewer = useStore(session)?.user.id ?? "";

  const q = useInfiniteQuery({
    queryKey: queryKey("manage", account, "links", filter.audience, filter.kind),
    queryFn: ({ pageParam }) => listAccountLinks(account, filter, pageParam, PAGE_SIZE),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const rows: AccountLink[] = (q.data?.pages ?? []).reduce<AccountLink[]>((shown, page, i) => mergeLinks(shown, page, i === 0), []);

  const count = useQuery({
    queryKey: queryKey("manage", account, "sharing-policy"),
    queryFn: () => getSharingPolicy(account),
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const anyoneCount = count.data?.anyone_links ?? 0;

  const refresh = () => qc.invalidateQueries({ queryKey: queryKey("manage", account, "links") });
  const refreshCount = () => qc.invalidateQueries({ queryKey: queryKey("manage", account, "sharing-policy") });

  const confirmDelete = () =>
    once(async () => {
      if (!deleting) return;
      const stale = sessionChanged();
      const link = deleting;
      setDeleteBusy(true);
      setDeleteError(null);
      try {
        await deleteShareLink(link.id);
        if (stale()) return;
        setDeleting(null);
        setNotice(t("links.deleted"));
        await refresh();
        await refreshCount();
      } catch (err) {
        if (stale()) return;
        setDeleteError(failureText(err, lang));
      } finally {
        setDeleteBusy(false);
      }
    });

  const setAudience = (v: AudienceFilter) => {
    setFilter((f) => ({ ...f, audience: v }));
    setNotice(null);
  };
  const setKind = (v: KindFilter) => {
    setFilter((f) => ({ ...f, kind: v }));
    setNotice(null);
  };

  const text = (l: Label): string => ("text" in l ? l.text : t(l.key, l.params));
  const failure = q.isError ? failureText(q.error, lang) : null;
  const loaded = q.data !== undefined;

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {t("links.title")}
        </h1>
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
          <label htmlFor="links-audience">{t("links.filter.audience")}</label>
          <Select
            id="links-audience"
            value={filter.audience}
            onChange={(v: AudienceFilter) => setAudience(v)}
            options={[
              { value: "all", label: t("links.filter.all") },
              { value: "organisation", label: t("links.audience.organisation") },
              { value: "anyone", label: t("links.audience.anyone") },
            ]}
            style={{ minWidth: 160 }}
          />
          <label htmlFor="links-kind">{t("links.filter.kind")}</label>
          <Select
            id="links-kind"
            value={filter.kind}
            onChange={(v: KindFilter) => setKind(v)}
            options={[
              { value: "all", label: t("links.filter.all") },
              { value: "access", label: t("links.kind.access") },
              { value: "drop", label: t("links.kind.drop") },
            ]}
            style={{ minWidth: 160 }}
          />
        </div>
      </div>

      <p style={{ margin: "0 0 8px", color: "var(--ant-color-text-secondary)" }}>{t("links.intro")}</p>

      {count.isError && <Alert type="error" showIcon role="alert" message={failureText(count.error, lang)} style={{ marginBottom: 12 }} />}
      {!count.isError && count.data && (
        <DeletePublicLinks account={account} count={anyoneCount} ondone={() => { setNotice(null); void refresh(); void refreshCount(); }} />
      )}

      {notice !== null && <p role="status" style={{ margin: "8px 0", fontSize: 13 }}>{notice}</p>}
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {!loaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {loaded && rows.length === 0 && failure === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>{t("links.empty")}</p>
      )}

      {rows.length > 0 && (
        <ul aria-label={t("links.title")} style={{ margin: 0, padding: 0, listStyle: "none" }}>
          {rows.map((l) => (
            <li
              key={l.id}
              className="link-row"
              style={{ display: "flex", flexWrap: "wrap", gap: 12, justifyContent: "space-between", alignItems: "start", padding: "12px 0", borderBlockEnd: "1px solid var(--ant-color-split)" }}
            >
              <div style={{ minInlineSize: 0 }}>
                <p style={{ margin: 0, overflowWrap: "anywhere" }}>
                  <bdi className="item-name" style={{ fontWeight: 600 }}>{text(itemLabel(l, false))}</bdi>
                  {pathLabel(l) !== "" && (
                    <span style={{ marginInlineStart: 8, fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{pathLabel(l)}</span>
                  )}
                </p>
                <p style={{ margin: 0, fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
                  <span>{text(folderLabel(l, viewer))}</span>
                  <span aria-hidden="true"> · </span>
                  <span>{text(audienceLabel(l))}</span>
                  <span aria-hidden="true"> · </span>
                  <span>{text(kindLabel(l))}</span>
                  {l.role && (
                    <>
                      <span aria-hidden="true"> · </span>
                      <span>{t(`role.${l.role}`)}</span>
                    </>
                  )}
                </p>
                <p style={{ margin: 0, fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
                  <span>{text(creatorLabel(l))}</span>
                  <span aria-hidden="true"> · </span>
                  <time dateTime={l.created_at}>{formatDate(l.created_at, lang)}</time>
                  <span aria-hidden="true"> · </span>
                  <span>{l.expires_at ? t("links.ends", { date: formatDate(l.expires_at, lang) }) : t("links.noEnd")}</span>
                  {statusKeys(l).map((s, i) => (
                    <span key={i}>
                      <span aria-hidden="true"> · </span>
                      <span>{text(s)}</span>
                    </span>
                  ))}
                </p>
              </div>
              <div style={{ flex: "none" }}>
                <Button
                  onClick={() => {
                    setDeleteError(null);
                    setDeleting(l);
                  }}
                >
                  {t("links.delete.action")}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}

      {q.hasNextPage && (
        <div style={{ display: "flex", justifyContent: "center", marginBlockStart: 16 }}>
          <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}

      {deleting && (
        <Confirm
          id="link-delete"
          title={t("links.delete.title")}
          message={t("links.delete.message")}
          action={t("links.delete.action")}
          danger
          busy={deleteBusy}
          error={deleteError}
          onconfirm={() => void confirmDelete()}
          onclose={() => setDeleting(null)}
        />
      )}
    </div>
  );
}

export default function LinksPage() {
  return (
    <OwnerOnly section="links">
      <LinksView />
    </OwnerOnly>
  );
}
