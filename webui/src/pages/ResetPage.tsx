// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button, Typography } from "antd";
import { establish, resetPassword } from "../lib/auth";
import { errorMessage, isLinkFailure, type Message } from "../lib/errors";
import { go } from "../lib/router";
import { useT } from "../lib/i18n";
import { passwordMinLength, brand } from "../lib/brand";
import { flash } from "../lib/session";
import { useStore, get } from "../lib/store";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import NewPasswordFields from "../components/NewPasswordFields";
import { readFragmentToken } from "./linkToken";
import { checkNewPassword } from "./passwordRules";

// ResetPage chooses a new password from a reset link and signs the person in. The token is read from
// the fragment, as on the verify page.
export default function ResetPage() {
  const { t } = useT();
  const minLength = useStore(passwordMinLength);
  const product = useStore(brand).name;
  const [token, setToken] = useState("");
  const [linkUsable, setLinkUsable] = useState(true);
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  const [error, setError] = useState<Message | null>(null);
  const [busy, setBusy] = useState(false);
  const read = useRef(false);

  useEffect(() => {
    if (read.current) return;
    read.current = true;
    const value = readFragmentToken();
    setToken(value);
    if (value === "") {
      setLinkUsable(false);
      setError({ key: "error.linkInvalid" });
    }
  }, []);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    const check = checkNewPassword(password, confirm, get(passwordMinLength));
    setPasswordError(check.password);
    setConfirmError(check.confirm);
    if (check.password || check.confirm) {
      document.getElementById(check.password ? "reset-password" : "reset-confirm")?.focus();
      return;
    }
    setBusy(true);
    try {
      const signed = await resetPassword(token, password);
      await establish(signed);
      flash.set("reset.done");
      go("/", { replace: true });
    } catch (err) {
      if (isLinkFailure(err)) setLinkUsable(false);
      setError(errorMessage(err, "link"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("reset.title")}</h1>
      <p>{t("reset.body", { product })}</p>
      <Alert message={error} />
      {linkUsable ? (
        <form onSubmit={submit} noValidate>
          <NewPasswordFields
            idPrefix="reset"
            password={password}
            confirm={confirm}
            onPassword={setPassword}
            onConfirm={setConfirm}
            labels={{ password: t("newPassword.label"), confirm: t("newPassword.confirm") }}
            passwordError={passwordError ? t(passwordError, { min: minLength }) : null}
            confirmError={confirmError ? t(confirmError) : null}
          />
          <Button type="primary" htmlType="submit" block loading={busy} disabled={busy}>
            {t("reset.submit")}
          </Button>
        </form>
      ) : (
        <p>
          <Typography.Link href="/forgot">{t("forgot.title")}</Typography.Link>
        </p>
      )}
    </AuthLayout>
  );
}
