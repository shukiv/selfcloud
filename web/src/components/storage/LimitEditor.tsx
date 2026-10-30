// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Input, Select } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { limitTextFor, parseAccountLimit, parseUserLimit, type LimitText } from "../../lib/storage/logic";
import type { LimitUnit } from "../../lib/quota";
import { useOnce } from "../manage/OwnerPage";

// LimitEditor sets a storage limit by its amount and unit. A person's limit (allowDefault) may follow the account
// default, which is stored as null; the default itself (allowDefault false) has no default to follow. Empty is the
// default for a person, and no limit for the default. "0" is no limit in both.
export interface LimitEditorProps {
  current: number | null;
  allowDefault: boolean;
  onsave: (value: number | null) => Promise<void>;
  oncancel: () => void;
}

export default function LimitEditor({ current, allowDefault, onsave, oncancel }: LimitEditorProps) {
  const { t } = useT();
  const lang = useLang();
  const [start] = useState(() => limitTextFor(current));
  const [amount, setAmount] = useState(start.amount);
  const [unit, setUnit] = useState<LimitUnit>(start.unit);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const once = useOnce();

  const submit = () =>
    once(async () => {
      const text: LimitText = { amount, unit };
      // A stored limit is shown rounded. Saved untouched, it would be written back rounded, so nothing is sent.
      if (current !== null && amount === start.amount && unit === start.unit) {
        oncancel();
        return;
      }
      const parsed = allowDefault ? parseUserLimit(text) : parseAccountLimit(text);
      if (!parsed.ok) {
        setError(t("storage.limitInvalid"));
        return;
      }
      setBusy(true);
      setError(null);
      try {
        await onsave(parsed.value);
      } catch (err) {
        setError(failureText(err, lang));
      } finally {
        setBusy(false);
      }
    });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
      style={{ display: "grid", gap: 12 }}
      noValidate
    >
      <div style={{ display: "flex", gap: 8 }}>
        <Input
          inputMode="decimal"
          autoComplete="off"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          aria-label={t("storage.limitAmount")}
          aria-describedby="limit-hint"
          style={{ flex: "1 1 auto", minInlineSize: 0 }}
        />
        <Select
          value={unit}
          onChange={(v: LimitUnit) => setUnit(v)}
          aria-label={t("storage.limitUnit")}
          options={[
            { value: "GB", label: "GB" },
            { value: "TB", label: "TB" },
          ]}
        />
      </div>
      <p id="limit-hint" style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>
        {allowDefault ? t("storage.limitHintPerson") : t("storage.limitHintDefault")}
      </p>
      {error && <Alert type="error" showIcon role="alert" message={error} />}
      <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
        <Button disabled={busy} onClick={oncancel}>
          {t("common.cancel")}
        </Button>
        <Button type="primary" htmlType="submit" loading={busy} disabled={busy}>
          {t("storage.saveLimit")}
        </Button>
      </div>
    </form>
  );
}
