// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Button, Checkbox, Modal } from "antd";
import type { ConflictChoice } from "../../lib/files/conflict";
import { useT } from "../../lib/i18n";

// Conflict asks what to do with a name that is already taken. Each choice answers at once, as the Svelte
// dialog does; in a batch the person may remember the choice for the rest. Closing the dialog skips the file.
// The replace choice takes focus when the dialog opens.
export default function Conflict({
  name,
  batch = false,
  onAnswer,
  onCancel,
}: {
  name: string;
  batch?: boolean;
  onAnswer: (choice: ConflictChoice, remember: boolean) => void;
  onCancel: () => void;
}) {
  const { t } = useT();
  const [remember, setRemember] = useState(false);
  const first = useRef<HTMLButtonElement>(null);
  // The dialog moves focus to itself as it opens, after this component's first effect, so the focus is set on the
  // next turn of the event loop.
  useEffect(() => {
    const id = setTimeout(() => first.current?.focus(), 0);
    return () => clearTimeout(id);
  }, []);
  return (
    <Modal
      open
      title={t("files.conflict.title")}
      onCancel={onCancel}
      maskClosable={false}
      footer={null}
      destroyOnHidden
      data-testid="conflict"
    >
      <p>{t("files.conflict.body", { name })}</p>
      {batch && (
        <p>
          <Checkbox checked={remember} onChange={(e) => setRemember(e.target.checked)}>
            {t("files.conflict.remember")}
          </Checkbox>
        </p>
      )}
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "flex-end", gap: 8 }}>
        <Button ref={first} type="primary" onClick={() => onAnswer("replace", remember)}>
          {t("files.conflict.replace")}
        </Button>
        <Button onClick={() => onAnswer("rename", remember)}>{t("files.conflict.rename")}</Button>
        <Button onClick={() => onAnswer("skip", remember)}>{t("files.conflict.skip")}</Button>
      </div>
    </Modal>
  );
}
