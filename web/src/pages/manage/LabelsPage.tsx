// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Tag } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { createLabelField, deleteLabelField, listLabelFields, updateLabelField, type LabelField, type LabelFieldBody, type LabelFieldPatch } from "../../lib/labels/api";
import { failureText } from "../../lib/manage/failure";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
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
  const once = useOnce();

  const q = useQuery({
    queryKey: queryKey("labels", "fields"),
    queryFn: async () => (await listLabelFields()).items,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const fields: LabelField[] = q.data ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: queryKey("labels") });

  // The delete dialog counts the values as they are when it opens: a value set since the list was loaded counts too.
  const askDelete = async (f: LabelField) => {
    setDeleteError(null);
    const { data } = await q.refetch();
    const fresh = data?.find((x) => x.id === f.id);
    if (fresh !== undefined) setDeleting(fresh);
    else await refresh();
  };

  const save = async (body: LabelFieldBody | LabelFieldPatch) => {
    const stale = sessionChanged();
    const target = editing;
    if (target === "new") await createLabelField(account, body as LabelFieldBody);
    else if (target) await updateLabelField(account, target.id, body as LabelFieldPatch);
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
      setDeleteBusy(true);
      setDeleteError(null);
      try {
        await deleteLabelField(account, target.id);
        if (stale()) return;
        setNotice(t("labels.deleted", { name: target.name }));
        setDeleting(null);
        await refresh();
      } catch (err) {
        if (stale()) return;
        setDeleteError(failureText(err, lang));
      } finally {
        setDeleteBusy(false);
      }
    });

  // A failed refetch keeps the list that is shown; a failed first load shows the error and no empty list.
  const loaded = q.data !== undefined;
  const failure = q.isError ? failureText(q.error, lang) : null;

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
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {!loaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {loaded && fields.length === 0 && failure === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>{t("labels.empty")}</p>
      )}

      {fields.length > 0 && (
        <ul aria-label={t("manage.labels.title")} style={{ margin: 0, padding: 0, listStyle: "none" }}>
          {fields.map((f) => (
            <li
              key={f.id}
              className="row"
              style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, padding: "12px 0", borderBlockEnd: "1px solid var(--ant-color-split)" }}
            >
              <div style={{ display: "grid", gap: 4, minWidth: 0, flex: "1 1 180px" }}>
                <p style={{ margin: 0, fontWeight: 600, overflowWrap: "anywhere" }}>
                  <bdi>{f.name}</bdi>
                </p>
                <p style={{ margin: 0, display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center", fontSize: 13 }}>
                  <span>{t(`labels.type.${f.type}`)}</span>
                  {f.restricted && <Tag>{t("labels.restrictedBadge")}</Tag>}
                  {f.values !== undefined && <span>{t("labels.valuesCount", { count: f.values })}</span>}
                </p>
              </div>
              <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                <Button size="small" onClick={() => setEditing(f)}>
                  {t("labels.edit")}
                  <span className="sr-only">: {f.name}</span>
                </Button>
                <Button
                  size="small"
                  danger
                  onClick={() => {
                    void askDelete(f);
                  }}
                >
                  {t("labels.delete")}
                  <span className="sr-only">: {f.name}</span>
                </Button>
              </div>
            </li>
          ))}
        </ul>
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
