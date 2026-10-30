// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Modal, Radio, Select } from "antd";
import { ApiError } from "../../lib/apiError";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { personLabel } from "../../lib/manage/people";
import { removePerson } from "../../lib/manage/api";
import type { Person } from "../../lib/manage/types";
import { sessionChanged } from "../../lib/session";
import Confirm from "../Confirm";
import { useOnce } from "./OwnerPage";

// RemoveDialog removes a person. When the person's My files hold files, the server refuses until the owner says what
// happens to them: they move to another active member, or they are deleted for good. The server does not say how many
// files there are, and neither does this dialog.
export interface RemoveDialogProps {
  account: string;
  person: Person;
  candidates: Person[];
  onclose: () => void;
  ondone: () => void;
}

type Files = { transfer_to?: string; delete_files?: boolean };

export default function RemoveDialog({ account, person, candidates, onclose, ondone }: RemoveDialogProps) {
  const { t } = useT();
  const lang = useLang();
  const [step, setStep] = useState<"confirm" | "files">("confirm");
  const [choice, setChoice] = useState<"move" | "delete">("move");
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const once = useOnce();
  const label = personLabel(person);
  const movable = candidates.filter((c) => c.id !== person.id && c.role === "member" && c.status === "active");
  const canGo = choice === "delete" || target !== "";

  const go = (files?: Files) =>
    once(async () => {
      const stale = sessionChanged();
      setBusy(true);
      setError(null);
      try {
        await removePerson(account, person.id, files);
        if (stale()) return;
        ondone();
      } catch (err) {
        if (stale()) return;
        // A conflict with no reason is the files question: the server did not remove the person.
        if (step === "confirm" && err instanceof ApiError && err.code === "conflict" && typeof err.details.reason !== "string") {
          setStep("files");
        } else {
          setError(failureText(err, lang));
        }
      } finally {
        setBusy(false);
      }
    });

  if (step === "confirm") {
    return (
      <Confirm
        id="remove"
        title={t("people.remove.title", { name: label })}
        message={t("people.remove.message", { name: label })}
        action={t("people.remove.action")}
        danger
        busy={busy}
        error={error}
        onconfirm={() => void go()}
        onclose={onclose}
      />
    );
  }

  const submit = () => {
    if (!canGo) return;
    if (choice === "move") void go({ transfer_to: target });
    else void go({ delete_files: true });
  };

  return (
    <Modal open title={t("people.remove.filesTitle", { name: label })} onCancel={busy ? undefined : onclose} maskClosable={false} footer={null} destroyOnClose>
      <form
        id="remove-files"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
        style={{ display: "grid", gap: 12 }}
      >
        <p style={{ margin: 0 }}>{t("people.remove.filesMessage")}</p>
        <div role="radiogroup" aria-label={t("people.remove.filesMessage")}>
        <Radio.Group name="files" value={choice} onChange={(e) => setChoice(e.target.value)} style={{ display: "grid", gap: 8 }}>
          <Radio value="move" disabled={movable.length === 0}>
            {t("people.remove.move")}
          </Radio>
          {choice === "move" && (
            <div style={{ display: "grid", gap: 4, paddingInlineStart: 24 }}>
              <label htmlFor="files-target">{t("people.remove.moveTo")}</label>
              <Select
                id="files-target"
                value={target}
                onChange={(v: string) => setTarget(v)}
                options={[
                  { value: "", label: t("people.remove.choose") },
                  ...movable.map((c) => ({ value: c.id, label: personLabel(c) })),
                ]}
              />
            </div>
          )}
          <Radio value="delete">{t("people.remove.deleteFiles")}</Radio>
        </Radio.Group>
        </div>
        {error && <Alert type="error" showIcon role="alert" message={error} />}
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" }}>
          <Button disabled={busy} onClick={onclose}>
            {t("common.cancel")}
          </Button>
          <Button type="primary" danger={choice === "delete"} htmlType="submit" loading={busy} disabled={busy || !canGo}>
            {t("people.remove.action")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
