// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Modal } from "antd";
import { useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { signInAsUser } from "../../lib/manage/api";
import { personLabel } from "../../lib/manage/people";
import type { Person } from "../../lib/manage/types";
import { adoptSession } from "../../lib/impersonation";
import { go } from "../../lib/router";
import { homeFor } from "../../lib/routes";
import { sessionChanged } from "../../lib/session";
import Field from "../Field";
import PasswordField from "../PasswordField";
import { foot, muted } from "./adminStyles";
import { useGuard } from "./hooks";

// SignInAs starts the admin's sign-in as a tenant user. The admin types its own password; the answer is the user's session,
// which lasts an hour. The admin's password is kept in this dialog's state only, and is gone when it closes.
export default function SignInAs({ accountID, person, onClose }: { accountID: string; person: Person; onClose: () => void }) {
  const { t } = useT();
  const lang = useLang();
  const flight = useGuard();
  const name = personLabel(person);
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
      document.getElementById("signin-as-admin")?.focus();
      return;
    }
    setAdminError(null);
    void flight(async () => {
      const changed = sessionChanged();
      setBusy(true);
      try {
        const signed = await signInAsUser(accountID, person.id, adminPassword);
        if (changed()) return;
        const me = await adoptSession(signed);
        go(homeFor(me.user), { replace: true });
      } catch (err) {
        if (changed()) return;
        setError(failureText(err, lang, "admin"));
      } finally {
        setBusy(false);
      }
    });
  };

  return (
    <Modal open title={t("tenant.signInAs.title", { name })} onCancel={onClose} maskClosable={false} footer={null} destroyOnClose>
      <form onSubmit={submit} noValidate style={{ display: "grid", gap: 16 }}>
        <p role="status" style={muted}>
          {t("tenant.signInAs.what", { name })}
        </p>
        <Field id="signin-as-admin" label={t("admin.password")} error={adminError}>
          <PasswordField
            id="signin-as-admin"
            value={adminPassword}
            onChange={setAdminPassword}
            autoComplete="current-password"
            invalid={!!adminError}
          />
        </Field>
        {error !== null && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={foot}>
          <Button onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy}>
            {t("tenant.signInAs.submit", { name })}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
