// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { Button, Input, Typography } from "antd";
import { cliApprove, cliLookup, type CliRequest } from "../lib/cli";
import { ApiError } from "../lib/api";
import { errorMessage, type Message } from "../lib/errors";
import { useT } from "../lib/i18n";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";

// CliApprovePage approves a terminal's sign-in (decision 125). The person types the code the terminal shows; the code is
// never in the address. The page shows the sign-in first, so the person can see the terminal's name, the address it came
// from, and when it started, and approves or leaves it. Nothing is approved on a wrong code, and a must-change user is
// told to change the password first.
export default function CliApprovePage() {
  const { t } = useT();
  const [code, setCode] = useState("");
  const [request, setRequest] = useState<CliRequest | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Message | null>(null);

  // problem turns a refusal into the sentence that says why. A wrong code and an expired one have their own sentences.
  function problem(err: unknown): Message {
    if (err instanceof ApiError) {
      if (err.details.reason === "wrong_code" || err.details.reason === "malformed_code") return { key: "cliApprove.wrong" };
      if (err.details.reason === "expired") return { key: "cliApprove.expired" };
      if (err.details.reason === "already_approved") return { key: "cliApprove.alreadyDone" };
      if (err.details.reason === "password_change_required") return { key: "cliApprove.mustChange" };
      if (err.status === 403 && err.code === "forbidden") return { key: "cliApprove.admin" };
    }
    return errorMessage(err, "login");
  }

  // lookup shows the sign-in the typed code names. A wrong code is refused here, with nothing approved.
  async function lookup(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      setRequest(await cliLookup(code));
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  // approve approves the shown sign-in for the person whose session this browser holds.
  async function approve() {
    setError(null);
    setBusy(true);
    try {
      await cliApprove(code);
      setDone(true);
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  // leave forgets the shown sign-in and the code. Nothing is sent, so the terminal's request stays until it ends.
  function leave() {
    setRequest(null);
    setCode("");
    setError(null);
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("cliApprove.title")}</h1>
      {done ? (
        <p>{t("cliApprove.done")}</p>
      ) : request === null ? (
        <form onSubmit={(e) => void lookup(e)}>
          <p>{t("cliApprove.body")}</p>
          <label htmlFor="cli-code" style={{ display: "block", marginBottom: 4 }}>{t("cliApprove.codeLabel")}</label>
          <Input
            id="cli-code"
            value={code}
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            maxLength={12}
            onChange={(e) => setCode(e.target.value)}
            style={{ marginBottom: 12, fontFamily: "monospace", letterSpacing: 2 }}
          />
          <Alert message={error} />
          <Button type="primary" htmlType="submit" block loading={busy} disabled={busy || code.trim() === ""}>
            {t("cliApprove.lookup")}
          </Button>
        </form>
      ) : (
        <div>
          <p>{t("cliApprove.check")}</p>
          <dl style={{ margin: "0 0 12px" }}>
            <dt>{t("cliApprove.client")}</dt>
            <dd style={{ margin: "0 0 8px" }}>{request.client_name}</dd>
            <dt>{t("cliApprove.ip")}</dt>
            <dd style={{ margin: "0 0 8px" }}>{request.client_ip}</dd>
            <dt>{t("cliApprove.agent")}</dt>
            <dd style={{ margin: "0 0 8px" }}>{request.user_agent}</dd>
            <dt>{t("cliApprove.started")}</dt>
            <dd style={{ margin: "0 0 8px" }}>{new Date(request.created_at).toLocaleString()}</dd>
          </dl>
          <p role="note" style={{ color: "var(--ant-color-text-secondary)", margin: "0 0 8px" }}>{t("cliApprove.named")}</p>
          <p role="note" style={{ fontWeight: 600, margin: "0 0 12px" }}>{t("cliApprove.warning")}</p>
          <p>{t("cliApprove.scope")}</p>
          <Alert message={error} />
          <Button type="primary" block loading={busy} disabled={busy} onClick={() => void approve()}>
            {t("cliApprove.approve")}
          </Button>
          <Typography.Paragraph style={{ marginTop: 12 }}>
            <Button type="link" onClick={leave}>{t("cliApprove.notMe")}</Button>
          </Typography.Paragraph>
        </div>
      )}
    </AuthLayout>
  );
}
