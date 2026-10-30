// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Checkbox, Input, Modal } from "antd";
import { useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { sessionChanged } from "../../lib/session";
import { createAccount, type NewAccount } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { limitBytes, type LimitUnit } from "../../lib/quota";
import type { Account, PersonReply } from "../../lib/manage/types";
import { useGuard } from "./hooks";
import { field, fieldset, foot, form as formStyle } from "./adminStyles";

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// NewTenant creates a tenant with its first owner. The owner gets the invitation by email, or a link the page shows once.
// The storage limit is in decimal units, as the meter shows it; "No limit" sets no quota.
export default function NewTenant({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (account: Account, owner: PersonReply | undefined) => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const flight = useGuard();
  const [name, setName] = useState("");
  const [noLimit, setNoLimit] = useState(false);
  const [amount, setAmount] = useState("");
  const [unit, setUnit] = useState<LimitUnit>("GB");
  const [email, setEmail] = useState("");
  const [ownerName, setOwnerName] = useState("");
  const [how, setHow] = useState<"email" | "link">("email");
  const [externalId, setExternalId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const nameOk = name.trim() !== "";
  const emailOk = EMAIL.test(email.trim());

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!nameOk || !emailOk) return;
    let quota = 0;
    if (!noLimit) {
      const bytes = limitBytes(amount, unit);
      if (bytes === null) {
        setError(t("tenant.new.limitInvalid"));
        return;
      }
      quota = bytes;
    }
    const body: NewAccount = {
      name: name.trim(),
      quota_bytes: quota,
      owner: { email: email.trim(), display_name: ownerName.trim(), verification: how },
    };
    if (externalId.trim() !== "") body.external_id = externalId.trim();
    void flight(async () => {
      const changed = sessionChanged();
      setBusy(true);
      setError(null);
      try {
        const created = await createAccount(body);
        if (changed()) return;
        onCreated(created, created.owner);
      } catch (err) {
        if (changed()) return;
        setError(failureText(err, lang));
      } finally {
        setBusy(false);
      }
    });
  };

  return (
    <Modal
      open
      title={t("tenant.new.title")}
      width={560}
      footer={null}
      maskClosable={false}
      keyboard={!busy}
      closable={!busy}
      onCancel={busy ? undefined : onClose}
    >
      <form style={formStyle} onSubmit={submit} noValidate>
        <div style={field}>
          <label htmlFor="new-tenant-name">{t("tenant.new.name")}</label>
          <Input id="new-tenant-name" autoComplete="off" maxLength={200} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </div>

        <fieldset style={fieldset}>
          <legend>{t("tenant.new.limit")}</legend>
          <div style={{ display: "flex", gap: 8 }}>
            <Input
              id="new-tenant-limit"
              inputMode="decimal"
              autoComplete="off"
              value={amount}
              disabled={noLimit}
              aria-label={t("tenant.new.limitAmount")}
              onChange={(e) => setAmount(e.target.value)}
              style={{ flex: "1 1 auto", minWidth: 0 }}
            />
            <select
              value={unit}
              disabled={noLimit}
              aria-label={t("tenant.new.limitUnit")}
              onChange={(e) => setUnit(e.target.value as LimitUnit)}
            >
              <option value="GB">GB</option>
              <option value="TB">TB</option>
            </select>
          </div>
          <Checkbox checked={noLimit} onChange={(e) => setNoLimit(e.target.checked)}>
            {t("tenant.new.noLimit")}
          </Checkbox>
        </fieldset>

        <fieldset style={fieldset}>
          <legend>{t("tenant.new.owner")}</legend>
          <div style={field}>
            <label htmlFor="new-tenant-owner-email">{t("tenant.new.ownerEmail")}</label>
            <Input id="new-tenant-owner-email" type="email" autoComplete="off" inputMode="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div style={field}>
            <label htmlFor="new-tenant-owner-name">{t("tenant.new.ownerName")}</label>
            <Input id="new-tenant-owner-name" autoComplete="off" value={ownerName} onChange={(e) => setOwnerName(e.target.value)} />
          </div>
          <fieldset style={fieldset}>
            <legend>{t("tenant.new.how")}</legend>
            <label>
              <input type="radio" name="how" value="email" checked={how === "email"} onChange={() => setHow("email")} />{" "}
              <span>{t("tenant.new.howEmail")}</span>
            </label>
            <label>
              <input type="radio" name="how" value="link" checked={how === "link"} onChange={() => setHow("link")} />{" "}
              <span>{t("tenant.new.howLink")}</span>
            </label>
          </fieldset>
        </fieldset>

        <details>
          <summary>{t("tenant.new.advanced")}</summary>
          <div style={field}>
            <label htmlFor="new-tenant-external">{t("tenant.new.externalId")}</label>
            <Input id="new-tenant-external" autoComplete="off" value={externalId} onChange={(e) => setExternalId(e.target.value)} />
            <p style={{ margin: 0, fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("tenant.new.externalHint")}</p>
          </div>
        </details>

        {error !== null && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={foot}>
          <Button disabled={busy} onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy || !nameOk || !emailOk}>
            {t("tenant.new.create")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
