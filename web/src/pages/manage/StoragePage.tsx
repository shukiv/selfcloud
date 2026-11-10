// SPDX-License-Identifier: AGPL-3.0-or-later
import { Pencil } from "lucide-react";
import { useState } from "react";
import { Alert, Button, Modal, Tag } from "antd";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate, formatNumber } from "../../lib/files/format";
import { failureText } from "../../lib/manage/failure";
import { personLabel } from "../../lib/manage/people";
import {
  accountLargest,
  accountStorage,
  setPersonLimit,
  setPolicyDefault,
  type SpaceFile,
  type StoragePerson,
} from "../../lib/storage/api";
import { heldBytes, LARGEST_PAGE, mergePage, PEOPLE_PAGE, sortByUse } from "../../lib/storage/logic";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import UsageMeter from "../../components/UsageMeter";
import LimitEditor from "../../components/storage/LimitEditor";
import { OwnerOnly, useAccountID, useManageTitle, useOnce } from "../../components/manage/OwnerPage";
import Listing from "../../components/files/Listing";

// Manage > Storage: the account's meter, the default limit for each person, the people by use with their own limits, and
// the team spaces with their largest files. An owner sees per-person totals but never a file name from a person's My
// files; the server leaves those out.
function StorageView() {
  const { t, tp } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  useManageTitle("manage.storage.title");
  const [notice, setNotice] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [editingDefault, setEditingDefault] = useState(false);
  const [editingPerson, setEditingPerson] = useState<StoragePerson | null>(null);
  // The team spaces sort by name, A to Z first; the header's click reverses it (decision 122).
  const [spaceDir, setSpaceDir] = useState<"asc" | "desc">("asc");
  const once = useOnce();

  const q = useInfiniteQuery({
    queryKey: queryKey("manage", account, "storage"),
    queryFn: ({ pageParam }) => accountStorage(account, pageParam, PEOPLE_PAGE),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.users_next_cursor ?? undefined,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const largest = useInfiniteQuery({
    queryKey: queryKey("manage", account, "storage-largest"),
    queryFn: ({ pageParam }) => accountLargest(account, { limit: LARGEST_PAGE, cursor: pageParam }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: account !== "",
    refetchOnMount: "always",
  });

  const first = q.data?.pages[0];
  const people: StoragePerson[] = sortByUse(mergePage([], (q.data?.pages ?? []).flatMap((p) => p.users ?? [])));
  const folders = (first?.folders ?? []).filter((f) => f.kind === "team");
  const rows: SpaceFile[] = largest.data?.pages.flatMap((p) => p.items) ?? [];
  const held = first ? heldBytes(first.account) : 0;
  const sign = spaceDir === "asc" ? 1 : -1;
  const spaces = [...folders].sort((a, b) => (a.name ?? "").localeCompare(b.name ?? "", lang) * sign);

  const refresh = () => qc.invalidateQueries({ queryKey: queryKey("manage", account, "storage") });

  const saveDefault = (value: number | null) =>
    once(async () => {
      const stale = sessionChanged();
      setActionError(null);
      try {
        await setPolicyDefault(account, value ?? 0);
        if (stale()) return;
        setEditingDefault(false);
        setNotice(t("storage.limitSaved"));
        await refresh();
      } catch (err) {
        if (stale()) return;
        // The editor shows a refusal where the owner is looking; the modal stays open for another try.
        throw err;
      }
    });

  const savePerson = (value: number | null) =>
    once(async () => {
      const p = editingPerson;
      if (!p) return;
      const stale = sessionChanged();
      setActionError(null);
      try {
        await setPersonLimit(account, p.id, value);
        if (stale()) return;
        setEditingPerson(null);
        setNotice(t("storage.limitSaved"));
        await refresh();
      } catch (err) {
        if (stale()) return;
        throw err;
      }
    });

  const limitText = (p: StoragePerson): string =>
    p.effective_quota_bytes === null ? t("storage.noLimit") : formatBytes(p.effective_quota_bytes, lang);

  const loading = q.isPending && account !== "";
  const failure = q.isError ? failureText(q.error, lang) : null;
  const loaded = q.data !== undefined;
  const sectionTitle = { fontSize: 16, fontWeight: 600, margin: "0 0 8px" } as const;
  const block = { marginBlockStart: 24 } as const;

  return (
    <div>
      <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>
        {t("manage.storage.title")}
      </h1>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {actionError !== null && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBottom: 12 }} />}
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {loading && <p role="status">{t("app.loading")}</p>}

      {first && (
        <>
          <section aria-labelledby="account-storage">
            <h2 id="account-storage" style={sectionTitle}>
              {t("storage.account")}
            </h2>
            <UsageMeter used={held} quota={first.account.quota_bytes} />
            <dl style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", margin: "12px 0 0" }}>
              <div>
                <dt>{t("storage.reserved")}</dt>
                <dd style={{ margin: 0 }}>{formatBytes(first.account.reserved_bytes, lang)}</dd>
              </div>
              <div>
                <dt>{t("storage.spacesCount")}</dt>
                <dd style={{ margin: 0 }}>{formatNumber(folders.length, lang)}</dd>
              </div>
            </dl>
          </section>

          <section aria-labelledby="default-limit" style={block}>
            <h2 id="default-limit" style={sectionTitle}>
              {t("storage.defaultTitle")}
            </h2>
            <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
              <p style={{ margin: 0 }}>
                {first.account.user_quota_bytes === 0
                  ? t("storage.defaultNone")
                  : t("storage.defaultLine", { size: formatBytes(first.account.user_quota_bytes, lang) })}
              </p>
              <Button size="small" onClick={() => setEditingDefault(true)}>
                {t("storage.change")}
              </Button>
            </div>
          </section>

          {/* The three storage tables are the one table (decision 122). Each section's name is its table's, so the section is not a
              second region with the same name. */}
          <section id="people-storage" style={block}>
            <h2 id="people-storage-title" style={sectionTitle}>
              {t("storage.people")}
            </h2>
            <Listing
              label={t("storage.people")}
              items={people}
              rowLabel={(p) => personLabel(p)}
              columns={[
                {
                  key: "person",
                  title: t("storage.person"),
                  render: (p) => (
                    <span style={{ display: "block" }}>
                      <bdi>{personLabel(p)}</bdi>
                      {p.email && p.email !== personLabel(p) && (
                        <span style={{ display: "block", color: "var(--ant-color-text-secondary)" }}>
                          <bdi>{p.email}</bdi>
                        </span>
                      )}
                    </span>
                  ),
                },
                {
                  key: "used",
                  title: t("storage.used"),
                  align: "end",
                  secondary: true,
                  render: (p) => <span style={{ whiteSpace: "nowrap" }}>{formatBytes(heldBytes(p), lang)}</span>,
                },
                {
                  key: "limit",
                  title: t("storage.limit"),
                  secondary: true,
                  render: (p) => (
                    <span>
                      {limitText(p)}
                      {p.quota_bytes === null && <span style={{ color: "var(--ant-color-text-secondary)" }}> · {t("storage.followsDefault")}</span>}
                    </span>
                  ),
                },
              ]}
              actions={(p) => [
                {
                  key: "edit",
                  label: `${t("storage.change")}: ${personLabel(p)}`,
                  icon: <Pencil size={18} aria-hidden="true" focusable="false" />,
                  run: () => setEditingPerson(p),
                },
              ]}
              empty={{ title: t("storage.peopleEmpty") }}
            />
            {q.hasNextPage && (
              <div style={{ display: "flex", justifyContent: "center", marginBlockStart: 12 }}>
                <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
                  {t("common.loadMore")}
                </Button>
              </div>
            )}
          </section>

          <section id="spaces-storage" style={block}>
            <h2 id="spaces-storage-title" style={sectionTitle}>
              {t("storage.spaces")}
            </h2>
            <Listing
              label={t("storage.spaces")}
              items={spaces}
              rowLabel={(f) => f.name ?? ""}
              columns={[
                { key: "space", title: t("storage.space"), sortKey: "name", render: (f) => <bdi>{f.name ?? ""}</bdi> },
                {
                  key: "size",
                  title: t("storage.size"),
                  align: "end",
                  secondary: true,
                  render: (f) => <span style={{ whiteSpace: "nowrap" }}>{formatBytes(f.used_bytes + f.reserved_bytes, lang)}</span>,
                },
                {
                  key: "files",
                  title: t("storage.files"),
                  align: "end",
                  secondary: true,
                  render: (f) => <span style={{ whiteSpace: "nowrap" }}>{tp("storage.fileCount", f.used_files)}</span>,
                },
              ]}
              // The team spaces are the whole list (the first page of the account's read), so the sort is on the client.
              sort={{ key: "name", dir: spaceDir, onSort: () => setSpaceDir((d) => (d === "asc" ? "desc" : "asc")) }}
              empty={{ title: t("storage.spacesEmpty") }}
            />
          </section>
        </>
      )}

      <section id="largest-spaces" style={block}>
        <h2 id="largest-spaces-title" style={sectionTitle}>
          {t("storage.spaceLargest")}
        </h2>
        {largest.isError && rows.length === 0 && (
          <Alert type="error" showIcon role="alert" message={failureText(largest.error, lang)} />
        )}
        {largest.isError && rows.length > 0 && (
          <Alert type="warning" showIcon role="alert" message={failureText(largest.error, lang)} style={{ marginBottom: 12 }} />
        )}
        {largest.data !== undefined && (
          <Listing
            label={t("storage.spaceLargest")}
            items={rows}
            rowLabel={(f) => f.name}
            columns={[
              {
                key: "name",
                title: t("storage.name"),
                render: (f) => (
                  <span>
                    <bdi>{f.name}</bdi>
                    {f.in_trash && (
                      <span style={{ marginInlineStart: 8 }}>
                        <Tag color="warning">{t("storage.inTrash")}</Tag>
                      </span>
                    )}
                  </span>
                ),
              },
              { key: "space", title: t("storage.space"), secondary: true, render: (f) => <bdi>{f.folder_name}</bdi> },
              {
                key: "size",
                title: t("storage.size"),
                align: "end",
                secondary: true,
                render: (f) => <span style={{ whiteSpace: "nowrap" }}>{formatBytes(f.size, lang)}</span>,
              },
              { key: "modified", title: t("storage.modified"), secondary: true, render: (f) => <time dateTime={f.modified_at}>{formatDate(f.modified_at, lang)}</time> },
            ]}
            empty={{ title: t("storage.largestEmpty") }}
          />
        )}
        {largest.hasNextPage && (
          <div style={{ display: "flex", justifyContent: "center", marginBlockStart: 12 }}>
            <Button disabled={largest.isFetchingNextPage} onClick={() => void largest.fetchNextPage()}>
              {t("common.loadMore")}
            </Button>
          </div>
        )}
      </section>

      {editingDefault && first && (
        <Modal open title={t("storage.defaultTitle")} onCancel={() => setEditingDefault(false)} maskClosable={false} footer={null} destroyOnClose>
          <LimitEditor
            current={first.account.user_quota_bytes === 0 ? null : first.account.user_quota_bytes}
            allowDefault={false}
            onsave={saveDefault}
            oncancel={() => setEditingDefault(false)}
          />
        </Modal>
      )}

      {editingPerson && (
        <Modal
          open
          title={t("storage.limitFor", { name: personLabel(editingPerson) })}
          onCancel={() => setEditingPerson(null)}
          maskClosable={false}
          footer={null}
          destroyOnClose
        >
          <LimitEditor
            current={editingPerson.quota_bytes}
            allowDefault
            onsave={savePerson}
            oncancel={() => setEditingPerson(null)}
          />
        </Modal>
      )}
    </div>
  );
}

export default function StoragePage() {
  return (
    <OwnerOnly section="storage">
      <StorageView />
    </OwnerOnly>
  );
}
