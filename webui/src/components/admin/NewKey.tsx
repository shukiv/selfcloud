// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Input, Modal, Select } from "antd";
import { useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { sessionChanged } from "../../lib/session";
import { createKey } from "../../lib/manage/api";
import { expiryFor, type ExpiryChoice } from "../../lib/manage/admin";
import { failureText } from "../../lib/manage/failure";
import type { NewKey as NewKeyReply } from "../../lib/manage/types";
import { useGuard } from "./hooks";
import { field, foot, form as formStyle } from "./adminStyles";

// NewKey makes an API key for a tenant. The key is handed up once, to be shown in a dialog that holds it only while open.
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
  const lang = useLang();
  const flight = useGuard();
  const [name, setName] = useState("");
  const [expires, setExpires] = useState<ExpiryChoice>("never");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim() === "") return;
    void flight(async () => {
      const changed = sessionChanged();
      setBusy(true);
      setError(null);
      try {
        const key = await createKey(account, { name: name.trim(), expires_at: expiryFor(expires, new Date()) });
        if (changed()) return;
        onCreated(key);
      } catch (err) {
        if (changed()) return;
        setError(failureText(err, lang));
      } finally {
        setBusy(false);
      }
    });
  };

  return (
    <Modal
      open
      title={t("keys.new.title")}
      footer={null}
      maskClosable={false}
      keyboard={!busy}
      closable={!busy}
      onCancel={busy ? undefined : onClose}
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
        {error !== null && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={foot}>
          <Button disabled={busy} onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy || name.trim() === ""}>
            {t("keys.new.create")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
