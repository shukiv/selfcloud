// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { App, Alert, Button, Input, Modal } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { useFlight } from "../../lib/flight";
import { useOnce } from "./OwnerPage";

// NameDialog asks for one name, to create or rename a group or a team space. The server checks it; a refusal is shown
// by its code. onsave resolves when the name is saved, and the dialog then closes; a failure keeps it open. onsave gets
// the signal of its request: a cancelled dialog aborts it.
export interface NameDialogProps {
  id: string;
  title: string;
  label: string;
  initial?: string;
  action: string;
  onsave: (name: string, signal: AbortSignal) => Promise<void>;
  onclose: () => void;
}

export default function NameDialog({ id, title, label, initial = "", action, onsave, onclose }: NameDialogProps) {
  const { t } = useT();
  const lang = useLang();
  const [name, setName] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const once = useOnce();
  const flight = useFlight();
  const { message } = App.useApp();
  const ok = name.trim() !== "" && name.trim().length <= 255;

  // cancel closes the dialog at once, busy or not. A save on its way is aborted, and the page says it may or may not have happened.
  const cancel = () => {
    if (flight.abort()) void message.warning(t("common.cancelledMaybe"));
    onclose();
  };

  const submit = () =>
    once(async () => {
      if (!ok) return;
      setBusy(true);
      setError(null);
      try {
        await onsave(name.trim(), flight.start());
      } catch (err) {
        setError(failureText(err, lang));
        setBusy(false);
      } finally {
        flight.end();
      }
    });

  return (
    <Modal open title={title} onCancel={cancel} maskClosable={false} footer={null} destroyOnClose>
      <form
        id={id}
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
        style={{ display: "grid", gap: 16 }}
        noValidate
      >
        <div style={{ display: "grid", gap: 4 }}>
          <label htmlFor={`${id}-name`}>{label}</label>
          <Input id={`${id}-name`} autoComplete="off" maxLength={255} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </div>
        {error && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" }}>
          <Button onClick={cancel}>
            {t("common.cancel")}
          </Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy || !ok}>
            {action}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
