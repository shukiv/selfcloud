// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button, Modal } from "antd";
import { useT } from "../lib/i18n";
import type { SwitchPrompt as Prompt } from "../lib/signin";
import Sentence from "./Sentence";

// SwitchPrompt is the question a browser signed in as one person gets when a sign-in link, a verification or a reset
// is for another (decision 74). The sign-in link, the verify page and the reset page all ask it the same way. Only its
// buttons answer it: a click outside the dialog does not.
export default function SwitchPrompt({ prompt, onConfirm, onStay }: {
  prompt: Prompt | null;
  onConfirm: () => void;
  onStay: () => void;
}) {
  const { t } = useT();
  const names: Record<string, string> = prompt ? { current: prompt.current, target: prompt.target } : {};
  return (
    <Modal
      open={prompt !== null}
      title={t("signin.switch.title")}
      onCancel={onStay}
      // A click outside answers a question about the session, so it must not answer it: only the buttons do.
      maskClosable={false}
      footer={null}
      destroyOnHidden
    >
      {prompt && (
        <div style={{ display: "grid", gap: 12 }}>
          <p style={{ margin: 0 }}>
            <Sentence template={t("signin.switch.message")} names={names} />
          </p>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" }}>
            <Button type="primary" onClick={onConfirm}>
              <Sentence template={t("signin.switch.confirm")} names={names} />
            </Button>
            <Button onClick={onStay}>
              <Sentence template={t("signin.switch.stay")} names={names} />
            </Button>
          </div>
        </div>
      )}
    </Modal>
  );
}
