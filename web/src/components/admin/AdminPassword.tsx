// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Modal } from "antd";
import { useState, type FormEvent, type ReactNode } from "react";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { sessionChanged } from "../../lib/session";
import Field from "../Field";
import PasswordField from "../PasswordField";
import { foot, muted } from "./adminStyles";
import { useGuard } from "./hooks";

// AdminPassword asks the installation admin for its own password before a change that the server checks it for (decision
// 41, decision 84): a sign-in as a user, a key made from an admin's session, a new link for an owner. The caller says what
// will happen (what) and sends the request in onConfirm, which receives the typed password. A refusal is shown here, in the
// prompt, and the prompt stays open. The password lives in this dialog's state only: it is gone when the dialog closes.
export default function AdminPassword({
  id,
  title,
  what,
  submitLabel,
  onConfirm,
  onClose,
}: {
  id: string;
  title: ReactNode;
  what: ReactNode;
  submitLabel: ReactNode;
  onConfirm: (adminPassword: string) => Promise<void>;
  onClose: () => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const flight = useGuard();
  const [adminPassword, setAdminPassword] = useState("");
  const [adminError, setAdminError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setError(null);
    if (adminPassword === "") {
      setAdminError(t("error.passwordRequired"));
      document.getElementById(id)?.focus();
      return;
    }
    setAdminError(null);
    void flight(async () => {
      const changed = sessionChanged();
      setBusy(true);
      try {
        await onConfirm(adminPassword);
      } catch (err) {
        if (changed()) return;
        setError(failureText(err, lang, "admin"));
      } finally {
        setBusy(false);
      }
    });
  };

  return (
    <Modal open title={title} onCancel={onClose} maskClosable={false} footer={null} destroyOnClose>
      <form onSubmit={submit} noValidate style={{ display: "grid", gap: 16 }}>
        <p role="status" style={muted}>
          {what}
        </p>
        <Field id={id} label={t("admin.password")} error={adminError}>
          <PasswordField id={id} value={adminPassword} onChange={setAdminPassword} autoComplete="current-password" invalid={!!adminError} />
        </Field>
        {error !== null && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={foot}>
          <Button onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy}>
            {submitLabel}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
