// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { Button, Input, Radio, Typography } from "antd";
import { ApiError } from "../lib/api";
import { establish, signIn } from "../lib/auth";
import { errorMessage, type Message } from "../lib/errors";
import { go } from "../lib/router";
import { afterSignIn } from "../lib/next";
import { useT } from "../lib/i18n";
import type { AccountChoice } from "../lib/types";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import Field, { describedBy } from "../components/Field";
import PasswordField from "../components/PasswordField";

// LoginPage signs in with an email and a password. An email in several accounts asks which one.
export default function LoginPage() {
  const { t } = useT();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [emailError, setEmailError] = useState<string | null>(null);
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [error, setError] = useState<Message | null>(null);
  const [accounts, setAccounts] = useState<AccountChoice[] | null>(null);
  const [account, setAccount] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setEmailError(null);
    setPasswordError(null);
    if (email.trim() === "") {
      setEmailError("error.emailRequired");
      document.getElementById("login-email")?.focus();
      return;
    }
    if (password === "") {
      setPasswordError("error.passwordRequired");
      document.getElementById("login-password")?.focus();
      return;
    }
    setBusy(true);
    // The next address is read before the session is set: the gate moves the page on as soon as it is, and the
    // address then no longer carries the next the person came from.
    const next = new URLSearchParams(window.location.search).get("next");
    try {
      const signed = await signIn(email.trim(), password, account || undefined);
      await establish(signed);
      go(afterSignIn(next), { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && Array.isArray(err.details.accounts)) {
        const choices = err.details.accounts as AccountChoice[];
        setAccounts(choices);
        setAccount(choices[0]?.id ?? "");
      } else {
        setError(errorMessage(err, "login"));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("login.title")}</h1>
      <Alert message={error} />
      <form onSubmit={submit} noValidate>
        <Field id="login-email" label={t("login.email")} error={emailError ? t(emailError) : null}>
          <Input
            id="login-email"
            type="email"
            inputMode="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            status={emailError ? "error" : undefined}
            aria-describedby={describedBy("login-email", null, emailError)}
          />
        </Field>
        <Field id="login-password" label={t("login.password")} error={passwordError ? t(passwordError) : null}>
          <PasswordField
            id="login-password"
            value={password}
            onChange={setPassword}
            autoComplete="current-password"
            invalid={!!passwordError}
            describedBy={describedBy("login-password", null, passwordError)}
          />
        </Field>
        <p style={{ margin: "0 0 16px" }}>
          <Typography.Link href="/forgot">{t("login.forgot")}</Typography.Link>
        </p>
        {accounts && (
          <fieldset style={{ border: 0, padding: 0, margin: "0 0 16px" }}>
            <legend style={{ fontWeight: 600, marginBottom: 6 }}>{t("login.pick.title")}</legend>
            <p style={{ marginTop: 0 }}>{t("login.pick.body")}</p>
            <Radio.Group
              value={account}
              onChange={(e) => setAccount(e.target.value as string)}
              style={{ display: "flex", flexDirection: "column", gap: 8 }}
            >
              {accounts.map((choice, i) => (
                <Radio key={choice.id} value={choice.id} id={`login-account-${i}`}>
                  <bdi>{choice.name}</bdi>
                </Radio>
              ))}
            </Radio.Group>
          </fieldset>
        )}
        <Button type="primary" htmlType="submit" block loading={busy} disabled={busy}>
          {accounts ? t("login.pick.submit") : t("login.submit")}
        </Button>
      </form>
    </AuthLayout>
  );
}
