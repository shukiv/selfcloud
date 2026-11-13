// SPDX-License-Identifier: AGPL-3.0-or-later
import { Pencil, Trash2 } from "lucide-react";

import { useMemo, useState } from "react";
import { App, Alert, Button, Tag } from "antd";
import { useFlight } from "../../lib/flight";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { createLabelField, deleteLabelField, listLabelFields, updateLabelField, type LabelField, type LabelFieldBody, type LabelFieldPatch } from "../../lib/labels/api";
import { failureText } from "../../lib/manage/failure";
import { useAdminText } from "../../lib/manage/adminText";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import Listing, { type ListColumn, type ListSort } from "../../components/files/Listing";
import Confirm from "../../components/Confirm";
import LabelDialog from "../../components/labels/LabelDialog";
import { OwnerOnly, useAccountID, useManageTitle, useOnce } from "../../components/manage/OwnerPage";

// Labels: the owner defines the fields that describe items. Every reader of an item sees its values; editors set them.
// A restricted field is seen and set only by the managers of an item. Deleting a field removes its values, so the
// question states how many items hold one first.
function LabelsView() {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  useManageTitle("manage.labels.title");
  const [notice, setNotice] = useState<string | null>(null);
  const [editing, setEditing] = useState<LabelField | "new" | null>(null);
  const [deleting, setDeleting] = useState<LabelField | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const flight = useFlight();
  const { message } = App.useApp();
  // cancelDelete closes the dialog at once, busy or not: the delete on its way is aborted, and the fields are read again (fu4).
  const cancelDelete = () => {
    if (flight.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      void refresh();
    }
    setDeleteError(null);
    setDeleting(null);
  };
  const once = useOnce();
  const adminText = useAdminText();

  const q = useQuery({
    queryKey: queryKey("labels", "fields"),
    queryFn: async () => (await listLabelFields()).items,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const loaded: LabelField[] = q.data ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: queryKey("labels") });

  // The fields are loaded whole, so Name sorts them here. The first order is A to Z by name.
  const [sort, setSort] = useState<{ key: "name"; dir: "asc" | "desc" }>({ key: "name", dir: "asc" });
  const sortBy = (key: string) => {
    if (key !== "name") return;
    setSort((s) => ({ key, dir: s.dir === "asc" ? "desc" : "asc" }));
  };
  const fields: LabelField[] = useMemo(() => {
    const sign = sort.dir === "asc" ? 1 : -1;
    return [...loaded].sort((a, b) => a.name.localeCompare(b.name, lang) * sign || a.id.localeCompare(b.id));
  }, [loaded, sort, lang]);
  const listSort: ListSort = { key: "name", dir: sort.dir, onSort: sortBy };

  // The delete dialog counts the values as they are when it opens: a value set since the list was loaded counts too.
  const askDelete = async (f: LabelField) => {
    setDeleteError(null);
    const { data } = await q.refetch();
    const fresh = data?.find((x) => x.id === f.id);
    if (fresh !== undefined) setDeleting(fresh);
    else await refresh();
  };

  const save = async (body: LabelFieldBody | LabelFieldPatch, signal?: AbortSignal) => {
    const stale = sessionChanged();
    const target = editing;
    if (target === "new") await createLabelField(account, body as LabelFieldBody, signal);
    else if (target) await updateLabelField(account, target.id, body as LabelFieldPatch, signal);
    if (stale()) return;
    setEditing(null);
    setNotice(t("labels.saved"));
    await refresh();
  };

  const confirmDelete = () =>
    once(async () => {
      if (!deleting) return;
      const stale = sessionChanged();
      const target = deleting;
      const signal = flight.start();
      setDeleteBusy(true);
      setDeleteError(null);
      try {
        await deleteLabelField(account, target.id, signal);
        if (stale()) return;
        setNotice(t("labels.deleted", { name: target.name }));
        setDeleting(null);
        await refresh();
      } catch (err) {
        if (stale() || signal.aborted) return;
        setDeleteError(failureText(err, lang));
      } finally {
        flight.end();
        setDeleteBusy(false);
      }
    });

  // A failed refetch keeps the list that is shown; a failed first load shows the error and no empty list.
  const isLoaded = q.data !== undefined;
  const failure = q.isError ? failureText(q.error, lang) : null;

  // The columns: Name sorts; on a phone the type, the restriction and the count are the name's second line.
  const columns: ListColumn<LabelField>[] = [
    { key: "name", title: t("files.col.name"), sortKey: "name", render: (f) => <bdi>{f.name}</bdi> },
    { key: "type", title: t("labels.typeLabel"), secondary: true, width: 140, render: (f) => t(`labels.type.${f.type}`) },
    {
      key: "restricted",
      title: t("manage.col.restricted"),
      secondary: true,
      width: 130,
      render: (f) => (f.restricted ? <Tag>{t("labels.restrictedBadge")}</Tag> : null),
    },
    {
      key: "values",
      title: t("manage.col.values"),
      secondary: true,
      align: "end",
      width: 150,
      render: (f) => <span style={{ fontVariantNumeric: "tabular-nums" }}>{f.values ?? ""}</span>,
    },
  ];

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {t("manage.labels.title")}
        </h1>
        <Button type="primary" onClick={() => setEditing("new")}>
          {t("labels.new")}
        </Button>
      </div>
      <p style={{ margin: "0 0 16px", color: "var(--ant-color-text-secondary)" }}>{t("labels.intro")}</p>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {failure !== null && <Alert type={isLoaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {!isLoaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {isLoaded && (
        <Listing
          label={t("manage.labels.title")}
          items={fields}
          rowLabel={(f) => f.name}
          columns={columns}
          sort={listSort}
          actions={(f) => [
            {
              key: "edit",
              label: `${t("labels.edit")}: ${f.name}`,
              icon: <Pencil size={18} aria-hidden="true" focusable="false" />,
              run: () => setEditing(f),
            },
            {
              key: "delete",
              label: `${t("labels.delete")}: ${f.name}`,
              icon: <Trash2 size={18} aria-hidden="true" focusable="false" />,
              danger: true,
              run: () => {
                void askDelete(f);
              },
            },
          ]}
          empty={{
            title: t("labels.empty"),
            description: adminText ? t("admin.empty.labels") : undefined,
            action: { label: t("labels.new"), onClick: () => setEditing("new") },
          }}
        />
      )}

      {editing !== null && (
        <LabelDialog
          id="label-form"
          field={editing === "new" ? null : editing}
          onsave={save}
          onclose={() => setEditing(null)}
        />
      )}

      {deleting && (
        <Confirm
          id="label-delete"
          title={t("labels.deleteTitle", { name: deleting.name })}
          message={t("labels.deleteMessage", { count: deleting.values ?? 0 })}
          action={t("labels.delete")}
          danger
          busy={deleteBusy}
          error={deleteError}
          onconfirm={() => void confirmDelete()}
          onclose={() => setDeleting(null)}
          oncancel={cancelDelete}
        />
      )}
    </div>
  );
}

export default function LabelsPage() {
  return (
    <OwnerOnly section="labels">
      <LabelsView />
    </OwnerOnly>
  );
}
