// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { Button } from "antd";
import { changePassword, loadMe } from "../lib/auth";
import { errorMessage, type Message } from "../lib/errors";
import { go } from "../lib/router";
import { useT } from "../lib/i18n";
import { passwordMinLength } from "../lib/brand";
import { flash } from "../lib/session";
import { useStore, get } from "../lib/store";
import Alert from "../components/Alert";
import Field, { describedBy } from "../components/Field";
import NewPasswordFields from "../components/NewPasswordFields";
import PasswordField from "../components/PasswordField";
import { checkNewPassword } from "./passwordRules";

// ChangePasswordPage replaces the signed-in person's password. The current one is asked first.
export default function ChangePasswordPage() {
  const { t } = useT();
  const minLength = useStore(passwordMinLength);
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [currentError, setCurrentError] = useState<string | null>(null);
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  const [error, setError] = useState<Message | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setCurrentError(current === "" ? "error.passwordRequired" : null);
    const check = checkNewPassword(password, confirm, get(passwordMinLength));
    setPasswordError(check.password);
    setConfirmError(check.confirm);
    if (current === "" || check.password || check.confirm) {
      document.getElementById(current === "" ? "change-current" : check.password ? "change-password" : "change-confirm")?.focus();
      return;
    }
    setBusy(true);
    try {
      await changePassword(current, password);
      // The server replaces this session's cookie, so the token in memory is read again.
      await loadMe();
      flash.set("change.done");
      go("/", { replace: true });
    } catch (err) {
      setError(errorMessage(err, "password"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div style={{ maxWidth: 560 }}>
      <h1 style={{ fontSize: 24 }}>{t("change.title")}</h1>
      <Alert message={error} />
      <form onSubmit={submit} noValidate>
        <Field id="change-current" label={t("change.current")} error={currentError ? t(currentError) : null}>
          <PasswordField
            id="change-current"
            value={current}
            onChange={setCurrent}
            autoComplete="current-password"
            invalid={!!currentError}
            describedBy={describedBy("change-current", null, currentError)}
          />
        </Field>
        <NewPasswordFields
          idPrefix="change"
          password={password}
          confirm={confirm}
          onPassword={setPassword}
          onConfirm={setConfirm}
          labels={{ password: t("change.new"), confirm: t("change.confirm") }}
          passwordError={passwordError ? t(passwordError, { min: minLength }) : null}
          confirmError={confirmError ? t(confirmError) : null}
        />
        <Button type="primary" htmlType="submit" loading={busy} disabled={busy}>
          {t("change.submit")}
        </Button>
      </form>
    </div>
  );
}
