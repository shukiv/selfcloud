// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { Button, Input, Typography } from "antd";
import { forgotPassword } from "../lib/auth";
import { errorMessage, type Message } from "../lib/errors";
import { useT } from "../lib/i18n";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import Field, { describedBy } from "../components/Field";

// ForgotPage asks for a reset link. The answer is the same whether or not the address has an account.
export default function ForgotPage() {
  const { t } = useT();
  const [email, setEmail] = useState("");
  const [emailError, setEmailError] = useState<string | null>(null);
  const [error, setError] = useState<Message | null>(null);
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setEmailError(null);
    if (email.trim() === "") {
      setEmailError("error.emailRequired");
      document.getElementById("forgot-email")?.focus();
      return;
    }
    setBusy(true);
    try {
      await forgotPassword(email.trim());
      setSent(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("forgot.title")}</h1>
      <p>{t("forgot.body")}</p>
      <Alert message={error} />
      {sent && <Alert message={{ key: "forgot.sent" }} tone="success" />}
      <form onSubmit={submit} noValidate>
        <Field id="forgot-email" label={t("login.email")} error={emailError ? t(emailError) : null}>
          <Input
            id="forgot-email"
            type="email"
            inputMode="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            status={emailError ? "error" : undefined}
            aria-describedby={describedBy("forgot-email", null, emailError)}
          />
        </Field>
        <Button type="primary" htmlType="submit" block loading={busy} disabled={busy}>
          {t("forgot.submit")}
        </Button>
      </form>
      <p style={{ marginBottom: 0 }}>
        <Typography.Link href="/login">{t("forgot.back")}</Typography.Link>
      </p>
    </AuthLayout>
  );
}
