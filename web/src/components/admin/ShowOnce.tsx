// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Input, Modal } from "antd";
import { useState } from "react";
import { useT } from "../../lib/i18n";
import { field, foot } from "./adminStyles";

// ShowOnce shows a secret the server gives once: an API key, or a link to pass on. The value lives only in the parent's
// state while this dialog is open. It is not stored, logged or put in the URL. The dialog closes only by its button, so a
// stray Escape cannot dismiss the secret before it is copied.
export default function ShowOnce({
  id,
  title,
  label,
  value,
  warning,
  onClosed,
}: {
  id: string;
  title: string;
  label: string;
  value: string;
  warning: string;
  onClosed: () => void;
}) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  const fieldId = `${id}-value`;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      // The clipboard is not available here; the text is selected, so it can be copied by hand.
      setCopied(false);
      document.getElementById(fieldId)?.focus();
      (document.getElementById(fieldId) as HTMLInputElement | null)?.select();
    }
  };

  return (
    <Modal open title={title} closable={false} keyboard={false} maskClosable={false} footer={null}>
      <div style={{ display: "grid", gap: 16 }}>
        <Alert type="warning" showIcon role="note" message={warning} />
        <div style={field}>
          <label htmlFor={fieldId}>{label}</label>
          <div style={{ display: "flex", gap: 8 }}>
            <Input
              id={fieldId}
              readOnly
              value={value}
              onFocus={(e) => e.currentTarget.select()}
              style={{ flex: "1 1 auto", minWidth: 0 }}
            />
            <Button onClick={() => void copy()}>{t("secret.copy")}</Button>
          </div>
          <p role="status" aria-live="polite" style={{ margin: 0 }}>
            {copied ? t("secret.copied") : ""}
          </p>
        </div>
        <div style={foot}>
          <Button type="primary" onClick={onClosed}>
            {t("secret.done")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
