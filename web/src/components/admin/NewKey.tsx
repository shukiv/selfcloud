// SPDX-License-Identifier: AGPL-3.0-or-later

import { App, Alert, Button, Input, Modal, Select } from "antd";
import { useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { useFlight } from "../../lib/flight";
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
  const guard = useGuard();
  const flight = useFlight();
  const { message } = App.useApp();
  const [name, setName] = useState("");
  const [expires, setExpires] = useState<ExpiryChoice>("never");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // cancel closes the dialog at once, busy or not. A key on its way is aborted; the page says it may or may not have been made.
  const cancel = () => {
    if (flight.abort()) void message.warning(t("common.cancelledMaybe"));
    onClose();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim() === "") return;
    void guard(async () => {
      const changed = sessionChanged();
      setBusy(true);
      setError(null);
      try {
        const key = await createKey(account, { name: name.trim(), expires_at: expiryFor(expires, new Date()) }, flight.start());
        if (changed()) return;
        onCreated(key);
      } catch (err) {
        if (changed()) return;
        setError(failureText(err, lang));
      } finally {
        flight.end();
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
        {error !== null && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={foot}>
          <Button onClick={cancel}>{t("common.cancel")}</Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy || name.trim() === ""}>
            {t("keys.new.create")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
