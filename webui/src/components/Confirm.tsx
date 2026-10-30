// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Input, Modal } from "antd";
import { useT } from "../lib/i18n";
import { typedNameMatches } from "../lib/manage/confirm";

// Confirm asks before a change that cannot be undone or that ends access. With requireName, the person types the name,
// and the button stays off until the text matches. The parent passes any error text it has; a busy confirm cannot be
// closed, so a request in flight is never left without its dialog.
export interface ConfirmProps {
  id: string;
  title: string;
  message: string;
  action: string;
  danger?: boolean;
  requireName?: string | null;
  busy?: boolean;
  error?: string | null;
  onconfirm: () => void;
  onclose: () => void;
}

export default function Confirm({
  id,
  title,
  message,
  action,
  danger = false,
  requireName = null,
  busy = false,
  error = null,
  onconfirm,
  onclose,
}: ConfirmProps) {
  const { t } = useT();
  const [typed, setTyped] = useState("");
  const ok = requireName === null || typedNameMatches(typed, requireName);
  return (
    <Modal
      open
      title={title}
      onCancel={busy ? undefined : onclose}
      maskClosable={false}
      footer={[
        <Button key="cancel" disabled={busy} onClick={onclose}>
          {t("common.cancel")}
        </Button>,
        <Button
          key="go"
          type="primary"
          danger={danger}
          loading={busy}
          disabled={busy || !ok}
          onClick={onconfirm}
        >
          {action}
        </Button>,
      ]}
    >
      <div id={id} style={{ display: "grid", gap: 12 }}>
        <p style={{ margin: 0 }}>{message}</p>
        {requireName !== null && (
          <div style={{ display: "grid", gap: 4 }}>
            <label htmlFor={`${id}-name`}>{t("confirm.typeName", { name: requireName })}</label>
            <Input id={`${id}-name`} autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
          </div>
        )}
        {error && <Alert type="error" showIcon role="alert" message={error} />}
      </div>
    </Modal>
  );
}
