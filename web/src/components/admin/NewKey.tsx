// SPDX-License-Identifier: AGPL-3.0-or-later

import { App, Button, Input, Modal, Select } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useT } from "../../lib/i18n";
import { useFlight } from "../../lib/flight";
import { sessionChanged } from "../../lib/session";
import { createKey } from "../../lib/manage/api";
import { expiryFor, type ExpiryChoice } from "../../lib/manage/admin";
import type { NewKey as NewKeyReply } from "../../lib/manage/types";
import AdminPassword from "./AdminPassword";
import { field, foot, form as formStyle } from "./adminStyles";

// NewKey makes an API key for a tenant. A key made from an admin's session needs the admin's own password (decision 84):
// Create key asks for it in AdminPassword, and the key is sent only with it. The key is handed up once, to be shown in a
// dialog that holds it only while open.
export default function NewKey({
  account,
  onClose,
  onCreated,
}: {
  account: string;
  onClose: () => void;
  onCreated: (key: NewKeyReply) => void;
}) {
  const { t } = useT();
  const flight = useFlight();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [expires, setExpires] = useState<ExpiryChoice>("never");
  const [confirming, setConfirming] = useState(false);

  // cancel closes the dialog at once, busy or not. A key on its way is aborted; the page says it may or may not have been made.
  const cancel = () => {
    if (flight.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      // The key may have been made: the lists are read again, so it shows.
      void qc.invalidateQueries();
    }
    onClose();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim() === "") return;
    setConfirming(true);
  };

  // create sends the key with the admin's password. A refusal, a wrong password among them, reaches AdminPassword, which
  // shows it and stays open.
  const create = async (adminPassword: string) => {
    const changed = sessionChanged();
    try {
      const key = await createKey(account, { name: name.trim(), expires_at: expiryFor(expires, new Date()), admin_password: adminPassword }, flight.start());
      if (changed()) return;
      setConfirming(false);
      onCreated(key);
    } finally {
      flight.end();
    }
  };

  return (
    <Modal
      open
      title={t("keys.new.title")}
      footer={null}
      maskClosable={false}
      onCancel={cancel}
    >
      <form style={formStyle} onSubmit={submit} noValidate>
        <div style={field}>
          <label htmlFor="new-key-name">{t("keys.new.name")}</label>
          <Input id="new-key-name" autoComplete="off" maxLength={200} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </div>
        <div style={field}>
          <label htmlFor="new-key-expires">{t("keys.new.expires")}</label>
          <Select
            id="new-key-expires"
            value={expires}
            onChange={(v: ExpiryChoice) => setExpires(v)}
            options={[
              { value: "never", label: t("keys.expires.never") },
              { value: "30", label: t("keys.expires.30") },
              { value: "90", label: t("keys.expires.90") },
              { value: "365", label: t("keys.expires.365") },
            ]}
          />
        </div>
        <div style={foot}>
          <Button onClick={cancel}>{t("common.cancel")}</Button>
          <Button type="primary" htmlType="submit" disabled={name.trim() === ""}>
            {t("keys.new.create")}
          </Button>
        </div>
      </form>
      {confirming && (
        <AdminPassword
          id="new-key-admin"
          title={t("admin.confirm.title")}
          what={t("keys.new.passwordWhat")}
          submitLabel={t("admin.confirm.submit")}
          onConfirm={create}
          onClose={() => setConfirming(false)}
        />
      )}
    </Modal>
  );
}
