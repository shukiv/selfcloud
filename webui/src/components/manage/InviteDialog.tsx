// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Alert, Button, Input, Modal, Radio, Select, type InputRef } from "antd";
import { LANGUAGES, NATIVE_NAMES, useLang, useT, type Lang } from "../../lib/i18n";
import { createPerson, type NewPerson } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { sessionChanged } from "../../lib/session";
import type { PersonReply } from "../../lib/manage/types";
import { useOnce } from "./OwnerPage";

// InviteDialog invites a person by email. The invitation is always by email for an owner: a link to pass on is for
// API keys and installation admins only. The answer's verification is never shown or kept: the mail carries it.
export interface InviteDialogProps {
  account: string;
  onclose: () => void;
  oncreated: (reply: PersonReply) => void;
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export default function InviteDialog({ account, onclose, oncreated }: InviteDialogProps) {
  const { t } = useT();
  const lang = useLang();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<"member" | "owner">("member");
  const [locale, setLocale] = useState<Lang>(lang);
  const [busy, setBusy] = useState(false);
  const emailRef = useRef<InputRef>(null);
  // antd moves the focus into the dialog as it opens, after the field has mounted. The focus moves to the email field
  // once that has happened, so the person can type at once.
  useEffect(() => {
    const timer = window.setTimeout(() => emailRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, []);
  const [error, setError] = useState<string | null>(null);
  const once = useOnce();
  const emailOk = EMAIL.test(email.trim());

  const submit = () =>
    once(async () => {
      if (!emailOk) return;
      // The session is watched from before the request: an answer for a person who has gone is not shown.
      const stale = sessionChanged();
      setBusy(true);
      setError(null);
      const body: NewPerson = { email: email.trim(), display_name: name.trim(), locale, role, verification: "email" };
      try {
        const reply = await createPerson(account, body);
        if (stale()) return;
        oncreated(reply);
      } catch (err) {
        if (stale()) return;
        setError(failureText(err, lang));
      } finally {
        setBusy(false);
      }
    });

  return (
    <Modal
      open
      title={t("people.invite.title")}
      onCancel={busy ? undefined : onclose}
      maskClosable={false}
      footer={null}
      width={560}
      destroyOnClose
    >
      <form
        id="invite"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
        style={{ display: "grid", gap: 16 }}
        noValidate
      >
        <div style={{ display: "grid", gap: 4 }}>
          <label htmlFor="invite-email">{t("people.invite.email")}</label>
          <Input
            ref={emailRef}
            id="invite-email"
            type="email"
            autoComplete="off"
            inputMode="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-describedby="invite-email-hint"
            aria-invalid={email !== "" && !emailOk ? true : undefined}
          />
          <p id="invite-email-hint" style={{ margin: 0, fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
            {t("people.invite.emailHint")}
          </p>
        </div>
        <div style={{ display: "grid", gap: 4 }}>
          <label htmlFor="invite-name">{t("people.invite.name")}</label>
          <Input id="invite-name" autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <fieldset style={{ display: "grid", gap: 8, margin: 0, padding: 0, border: 0 }}>
          <legend style={{ marginBlockEnd: 4 }}>{t("people.invite.role")}</legend>
          <Radio.Group value={role} onChange={(e) => setRole(e.target.value)} name="invite-role">
            <Radio value="member">{t("people.role.member")}</Radio>
            <Radio value="owner">{t("people.role.owner")}</Radio>
          </Radio.Group>
          {role === "owner" && <Alert type="warning" showIcon role="note" message={t("people.invite.ownerWarning")} />}
        </fieldset>
        <div style={{ display: "grid", gap: 4 }}>
          <label htmlFor="invite-locale">{t("people.invite.language")}</label>
          <Select
            id="invite-locale"
            value={locale}
            onChange={(v: Lang) => setLocale(v)}
            options={LANGUAGES.map((l) => ({ value: l, label: NATIVE_NAMES[l] }))}
          />
        </div>
        {error && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" }}>
          <Button disabled={busy} onClick={onclose}>
            {t("common.cancel")}
          </Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy || !emailOk}>
            {t("people.invite.send")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
