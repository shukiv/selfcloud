// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Form, Input, Modal } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { createShortcut } from "../../lib/files/api";
import { fileErrorText } from "../../lib/files/fileErrors";
import { nameMessageKey, nameProblem } from "../../lib/files/ops";
import type { Node } from "../../lib/files/types";
import { sessionChanged, signingOut } from "../../lib/session";
import type { Destination } from "./PickDestination";

// AddShortcut asks for the name a shortcut will have, before it is made. The name starts as the target's own and can be
// changed: it is the name the readers of the destination folder will see, so the creator sees it first. The server
// answers a taken name, and the dialog keeps open to show it.
export default function AddShortcut({
  target,
  dest,
  onDone,
  onClose,
}: {
  target: Node;
  dest: Destination;
  onDone: (made: Node) => void;
  onClose: () => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const [name, setName] = useState(target.name);
  const [problem, setProblem] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    const clean = name.normalize("NFC").trim();
    const bad = nameProblem(name);
    if (bad) {
      setProblem(t(nameMessageKey(bad)));
      return;
    }
    if (!dest.dir) return;
    const changed = sessionChanged();
    setBusy(true);
    setError(null);
    try {
      const made = await createShortcut(dest.dir, target.id, clean);
      if (changed()) return;
      onDone(made);
    } catch (err) {
      if (!changed() && !signingOut()) setError(fileErrorText(err, lang));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      title={t("files.shortcut.title")}
      okText={t("files.shortcut.submit")}
      cancelText={t("common.cancel")}
      onCancel={onClose}
      onOk={() => void submit()}
      okButtonProps={{ disabled: busy }}
      maskClosable={false}
      destroyOnHidden
    >
      <Form layout="vertical" onFinish={() => void submit()}>
        <Form.Item label={t("files.shortcut.name")} validateStatus={problem ? "error" : undefined} help={problem ?? t("files.shortcut.hint")}>
          <Input
            id="shortcut-name"
            aria-label={t("files.shortcut.name")}
            value={name}
            disabled={busy}
            aria-invalid={problem ? true : undefined}
            onChange={(e) => {
              setName(e.target.value);
              setProblem(null);
            }}
          />
        </Form.Item>
      </Form>
      {error && (
        <div className="shortcut-error" style={{ marginTop: 12 }}>
          <Alert type="error" showIcon role="alert" message={error} />
        </div>
      )}
    </Modal>
  );
}
