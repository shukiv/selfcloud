// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "antd";
import { homeFor } from "../lib/routes";
import { establish, verifyAccount } from "../lib/auth";
import { errorMessage, isLinkFailure, type Message } from "../lib/errors";
import { go } from "../lib/router";
import { switchPromptOf, type SwitchPrompt as SwitchQuestion } from "../lib/signin";
import { useT } from "../lib/i18n";
import { passwordMinLength, brand } from "../lib/brand";
import { useStore, get } from "../lib/store";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import NewPasswordFields from "../components/NewPasswordFields";
import SwitchPrompt from "../components/SwitchPrompt";
import { readFragmentToken } from "./linkToken";
import { checkNewPassword } from "./passwordRules";

// VerifyPage sets the password of a new account and signs it in. The token is in the address's
// fragment: it is read once, and removed from the address bar, before anything else happens.
export default function VerifyPage() {
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
  const [prompt, setPrompt] = useState<SwitchQuestion | null>(null);
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
      document.getElementById(check.password ? "verify-password" : "verify-confirm")?.focus();
      return;
    }
    await send(false);
  }

  // send posts the form. A browser signed in as another person is asked first (decision 74); confirm accepts the switch.
  async function send(confirm: boolean) {
    setPrompt(null);
    setBusy(true);
    try {
      const signed = await verifyAccount(token, password, confirm);
      await establish(signed);
      go(homeFor(signed.user), { replace: true });
    } catch (err) {
      const asked = switchPromptOf(err);
      if (asked) {
        setPrompt(asked);
        return;
      }
      if (isLinkFailure(err)) setLinkUsable(false);
      setError(errorMessage(err, "link"));
    } finally {
      setBusy(false);
    }
  }

  // stay keeps the browser as it is, signed in as its own person, and does not use the link.
  function stay() {
    setPrompt(null);
    go("/files", { replace: true });
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("verify.title")}</h1>
      <p>{t("verify.body", { product })}</p>
      <Alert message={error} />
      <SwitchPrompt prompt={prompt} onConfirm={() => void send(true)} onStay={stay} />
      {linkUsable && (
        <form onSubmit={submit} noValidate>
          <NewPasswordFields
            idPrefix="verify"
            password={password}
            confirm={confirm}
            onPassword={setPassword}
            onConfirm={setConfirm}
            labels={{ password: t("newPassword.label"), confirm: t("newPassword.confirm") }}
            passwordError={passwordError ? t(passwordError, { min: minLength }) : null}
            confirmError={confirmError ? t(confirmError) : null}
          />
          <Button type="primary" htmlType="submit" block loading={busy} disabled={busy}>
            {t("verify.submit")}
          </Button>
        </form>
      )}
    </AuthLayout>
  );
}
