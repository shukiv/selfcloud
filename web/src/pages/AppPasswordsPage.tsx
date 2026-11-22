// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { Plus, Trash2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert as AntdAlert, Button, Input, Modal, Popconfirm, Spin } from "antd";
import Alert from "../components/Alert";
import IconButton from "../components/IconButton";
import Listing, { type ListColumn } from "../components/files/Listing";
import ShowOnce from "../components/admin/ShowOnce";
import { field, foot, form as formStyle } from "../components/admin/adminStyles";
import { createAppPassword, listAppPasswords, revokeAppPassword, type AppPassword } from "../lib/account/appPasswords";
import { ApiError } from "../lib/api";
import { errorMessage, type Message } from "../lib/errors";
import { useLang, useT } from "../lib/i18n";
import { formatDate } from "../lib/files/format";
import { queryKey } from "../lib/query";
import { session } from "../lib/session";
import { useStore } from "../lib/store";

// AppPasswordsPage is the account-menu page of the person's app passwords (decision 236): the credentials that sign a
// computer's network-drive mount in to their files. It is not a Manage page. The list says whether the drive mount is on
// (mount_enabled, the live dav.enabled); while it is off the page says so and offers no New, but the table and Revoke keep
// working so a leaked app password can always be ended. A made secret is shown once, in ShowOnce, and never again.
export default function AppPasswordsPage() {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const signedIn = useStore(session) !== null;
  const listKey = queryKey("app-passwords");

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);
  const [createError, setCreateError] = useState<Message | null>(null);
  const [shown, setShown] = useState<{ app_password: AppPassword; secret: string } | null>(null);
  const [notice, setNotice] = useState<Message | null>(null);
  const [error, setError] = useState<Message | null>(null);

  const q = useQuery({
    queryKey: listKey,
    queryFn: listAppPasswords,
    enabled: signedIn,
    refetchOnMount: "always",
  });

  const create = useMutation({
    mutationFn: (trimmed: string) => createAppPassword(trimmed),
    onSuccess: async (reply) => {
      setCreating(false);
      setName("");
      setNameError(null);
      setCreateError(null);
      setShown(reply);
      await qc.invalidateQueries({ queryKey: listKey });
    },
    onError: (err) => {
      // Turned off meanwhile: the mount-off alert takes over and the list is read again, so the form closes.
      if (err instanceof ApiError && err.status === 404) {
        setCreating(false);
        setName("");
        setNameError(null);
        setCreateError(null);
        void qc.invalidateQueries({ queryKey: listKey });
        return;
      }
      if (err instanceof ApiError && err.status === 409 && err.details.reason === "limit") {
        setCreateError({ key: "apppw.limit" });
        return;
      }
      setCreateError(errorMessage(err, "generic"));
    },
  });

  const revoke = useMutation({
    mutationFn: (id: string) => revokeAppPassword(id),
    onSuccess: async () => {
      setError(null);
      setNotice({ key: "apppw.revoked" });
      await qc.invalidateQueries({ queryKey: listKey });
    },
    onError: (err) => {
      setError(errorMessage(err, "generic"));
    },
  });

  const openCreate = () => {
    setNotice(null);
    setError(null);
    setCreateError(null);
    setNameError(null);
    setName("");
    setCreating(true);
  };

  const closeCreate = () => {
    setCreating(false);
    setName("");
    setNameError(null);
    setCreateError(null);
  };

  // The name is validated before anything is sent: required, trimmed, at most 64 characters.
  const submitCreate = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (trimmed === "") {
      setNameError("apppw.nameRequired");
      return;
    }
    if (trimmed.length > 64) {
      setNameError("apppw.nameLong");
      return;
    }
    setNameError(null);
    setCreateError(null);
    create.mutate(trimmed);
  };

  if (q.isPending) {
    return (
      <div style={{ textAlign: "center", padding: 24 }}>
        <Spin />
      </div>
    );
  }

  const mountOn = q.data?.mount_enabled === true;
  const rows = q.data?.items ?? [];

  const columns: ListColumn<AppPassword>[] = [
    { key: "name", title: t("apppw.name"), render: (a) => <bdi>{a.name}</bdi> },
    { key: "access", title: t("apppw.access"), render: (a) => (a.access === "write" ? t("apppw.readWrite") : t("apppw.readOnly")) },
    { key: "created", title: t("apppw.created"), render: (a) => formatDate(a.created_at, lang) },
    { key: "lastUsed", title: t("apppw.lastUsed"), render: (a) => (a.last_used_at ? formatDate(a.last_used_at, lang) : t("apppw.never")) },
    {
      key: "actions",
      title: t("storage.actions"),
      align: "end",
      width: 80,
      render: (a) => (
        <Popconfirm
          title={t("apppw.revokeConfirm", { name: a.name })}
          okText={t("apppw.revoke")}
          cancelText={t("common.cancel")}
          okButtonProps={{ danger: true }}
          onConfirm={() => revoke.mutate(a.id)}
        >
          <IconButton
            label={t("apppw.revokeLabel", { name: a.name })}
            tip={t("apppw.revoke")}
            icon={<Trash2 size={18} aria-hidden="true" focusable="false" />}
            danger
          />
        </Popconfirm>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, flexWrap: "wrap", margin: "0 0 4px" }}>
        <h1 style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>{t("apppw.title")}</h1>
        {mountOn && (
          <Button type="primary" icon={<Plus size={16} aria-hidden="true" focusable="false" />} onClick={openCreate}>
            {t("apppw.new")}
          </Button>
        )}
      </div>
      <p style={{ margin: "0 0 16px" }}>{t("apppw.intro")}</p>

      {!mountOn && <AntdAlert type="info" role="status" showIcon message={t("apppw.mountOff")} style={{ marginBlockEnd: 16 }} />}
      <Alert message={notice} tone="success" />
      <Alert message={error} />
      {q.isError && <Alert message={errorMessage(q.error, "generic")} />}

      <Listing<AppPassword>
        label={t("apppw.title")}
        items={rows}
        rowLabel={(a) => a.name}
        columns={columns}
        empty={{
          title: t("apppw.empty"),
          description: t("apppw.emptyText"),
          action: mountOn ? { label: t("apppw.new"), onClick: openCreate } : undefined,
        }}
      />

      {creating && (
        <Modal open title={t("apppw.newTitle")} footer={null} maskClosable={false} onCancel={closeCreate} destroyOnClose>
          <form style={formStyle} onSubmit={submitCreate} noValidate>
            <div style={field}>
              <label htmlFor="apppw-name">{t("apppw.name")}</label>
              <Input
                id="apppw-name"
                autoComplete="off"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  setNameError(null);
                }}
                autoFocus
              />
              {nameError && (
                <div role="alert" style={{ color: "var(--ant-color-error)" }}>
                  {t(nameError)}
                </div>
              )}
            </div>
            {createError && <Alert message={createError} />}
            <div style={foot}>
              <Button onClick={closeCreate}>{t("common.cancel")}</Button>
              <Button type="primary" htmlType="submit" loading={create.isPending}>
                {t("apppw.create")}
              </Button>
            </div>
          </form>
        </Modal>
      )}

      {shown !== null && (
        <ShowOnce
          id="apppw-secret"
          title={t("apppw.shown.title")}
          label={t("apppw.shown.label")}
          value={shown.secret}
          warning={t("apppw.shown.warning")}
          onClosed={() => setShown(null)}
        />
      )}
    </div>
  );
}
