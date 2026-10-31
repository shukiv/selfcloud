// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Modal, Radio, Space } from "antd";
import { useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { get, useStore } from "../../lib/store";
import { passwordMinLength } from "../../lib/brand";
import { failureText } from "../../lib/manage/failure";
import { resetUserPassword, setUserPassword } from "../../lib/manage/api";
import { personLabel } from "../../lib/manage/people";
import type { Person } from "../../lib/manage/types";
import { sessionChanged } from "../../lib/session";
import { checkNewPassword } from "../../pages/passwordRules";
import Field from "../Field";
import NewPasswordFields from "../NewPasswordFields";
import PasswordField from "../PasswordField";
import { foot, muted } from "./adminStyles";
import { useGuard } from "./hooks";

// The ways to reset, in the order the dialog offers them. Email needs mail; the other two need admin.tenant_access.
type Method = "email" | "link" | "typed";

// ResetPassword resets a tenant user's password. The dialog offers only the ways the installation allows, and says what
// will happen before the admin confirms. The typed password and the admin's own password live in this dialog's state
// only: they are gone when it closes. A link is handed to the parent, which shows it once.
export default function ResetPassword({
  accountID,
  person,
  mailConfigured,
  tenantAccess,
  onClose,
  onDone,
  onLink,
}: {
  accountID: string;
  person: Person;
  mailConfigured: boolean;
  tenantAccess: boolean;
  onClose: () => void;
  onDone: (notice: string) => void;
  onLink: (url: string) => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const flight = useGuard();
  const minLength = useStore(passwordMinLength);
  const name = personLabel(person);
  const offered: Method[] = [...(mailConfigured ? (["email"] as const) : []), ...(tenantAccess ? (["link", "typed"] as const) : [])];
  const [method, setMethod] = useState<Method>(offered[0] ?? "email");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [adminPassword, setAdminPassword] = useState("");
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  const [adminError, setAdminError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setError(null);
    // A link and a typed password both hand the account over, so both need the admin's own password (decision 41).
    if (method === "typed") {
      const check = checkNewPassword(password, confirm, get(passwordMinLength));
      setPasswordError(check.password ? t(check.password, { min: minLength }) : null);
      setConfirmError(check.confirm ? t(check.confirm) : null);
      setAdminError(adminPassword === "" ? t("error.passwordRequired") : null);
      if (check.password || check.confirm || adminPassword === "") {
        document
          .getElementById(check.password ? "reset-password" : check.confirm ? "reset-confirm" : "reset-admin")
          ?.focus();
        return;
      }
    } else if (method === "link") {
      setAdminError(adminPassword === "" ? t("error.passwordRequired") : null);
      if (adminPassword === "") {
        document.getElementById("reset-admin")?.focus();
        return;
      }
    }
    void flight(async () => {
      const changed = sessionChanged();
      setBusy(true);
      try {
        if (method === "typed") {
          await setUserPassword(accountID, person.id, password, adminPassword);
          if (changed()) return;
          onDone(t("tenant.users.passwordSet"));
          return;
        }
        const reply = await resetUserPassword(accountID, person.id, method, method === "link" ? adminPassword : undefined);
        if (changed()) return;
        if (method === "link" && reply.url) {
          onLink(reply.url);
        } else {
          onDone(reply.sent === false ? t("people.mailNotSent") : t("people.mailSent"));
        }
      } catch (err) {
        if (changed()) return;
        setError(failureText(err, lang, method === "email" ? "generic" : "admin"));
      } finally {
        setBusy(false);
      }
    });
  };

  const whatHappens =
    method === "email"
      ? t("tenant.reset.emailWhat", { email: person.email })
      : method === "link"
        ? t("tenant.reset.linkWhat")
        : t("tenant.reset.typedWhat");
  const submitLabel = method === "email" ? t("tenant.reset.submitEmail") : method === "link" ? t("tenant.reset.submitLink") : t("tenant.reset.submitTyped");
  const label: Record<Method, string> = { email: t("tenant.reset.email"), link: t("tenant.reset.link"), typed: t("tenant.reset.typed") };

  return (
    <Modal open title={t("tenant.reset.title", { name })} onCancel={onClose} maskClosable={false} footer={null} destroyOnClose>
      {offered.length === 0 ? (
        <Alert type="info" showIcon message={t("tenant.reset.none")} />
      ) : (
        <form onSubmit={submit} noValidate style={{ display: "grid", gap: 16 }}>
          <fieldset style={{ border: 0, margin: 0, padding: 0, minWidth: 0 }}>
            <legend style={{ fontWeight: 600, marginBlockEnd: 8 }}>{t("tenant.reset.method")}</legend>
            <Radio.Group value={method} onChange={(e) => setMethod(e.target.value as Method)}>
              <Space direction="vertical">
                {offered.map((m) => (
                  <Radio key={m} value={m}>
                    {label[m]}
                  </Radio>
                ))}
              </Space>
            </Radio.Group>
          </fieldset>
          <p role="status" style={muted}>
            {whatHappens}
          </p>
          {(method === "typed" || method === "link") && (
            <>
              {method === "typed" && (
                <NewPasswordFields
                  idPrefix="reset"
                  password={password}
                  confirm={confirm}
                  onPassword={setPassword}
                  onConfirm={setConfirm}
                  labels={{ password: t("change.new"), confirm: t("change.confirm") }}
                  passwordError={passwordError}
                  confirmError={confirmError}
                />
              )}
              <Field id="reset-admin" label={t("admin.password")} error={adminError}>
                <PasswordField
                  id="reset-admin"
                  value={adminPassword}
                  onChange={setAdminPassword}
                  autoComplete="current-password"
                  invalid={!!adminError}
                />
              </Field>
            </>
          )}
          {error !== null && <Alert type="error" showIcon role="alert" message={error} />}
          <div style={foot}>
            <Button onClick={onClose}>{t("common.cancel")}</Button>
            <Button type="primary" htmlType="submit" loading={busy} disabled={busy}>
              {submitLabel}
            </Button>
          </div>
        </form>
      )}
    </Modal>
  );
}
