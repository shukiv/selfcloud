// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Checkbox, Input, Modal, Select } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { sessionChanged } from "../../lib/session";
import type { LabelChoice, LabelField, LabelFieldBody, LabelFieldPatch, LabelType } from "../../lib/labels/api";
import { choiceProblem, LABEL_TYPES, nameProblem, labelFailure, type ChoiceRow } from "../../lib/labels/logic";
import { useOnce } from "../manage/OwnerPage";

// LabelDialog defines a label field, or changes one. The type is locked while the field holds values, and the server
// says so too. A choice that is archived keeps its values; it is never removed here. onsave resolves when the server has
// the change; a refusal keeps the dialog open and shows why.
export interface LabelDialogProps {
  id: string;
  field: LabelField | null;
  onsave: (body: LabelFieldBody | LabelFieldPatch) => Promise<void>;
  onclose: () => void;
}

export default function LabelDialog({ id, field, onsave, onclose }: LabelDialogProps) {
  const { t } = useT();
  const lang = useLang();
  const editing = field !== null;
  const [name, setName] = useState(field?.name ?? "");
  const [type, setType] = useState<LabelType>(field?.type ?? "text");
  const [restricted, setRestricted] = useState(field?.restricted ?? false);
  const [rows, setRows] = useState<ChoiceRow[]>(() =>
    (field?.choices ?? []).map((c: LabelChoice) => ({ id: c.id, name: c.name, archived: c.archived })),
  );
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const once = useOnce();
  const locked = editing && (field?.values ?? 0) > 0;
  const choosing = type === "choice";

  // The first problem is said under the form, and disables Save. The server checks the same things again.
  const namePb = nameProblem(name);
  const choicePb = choosing ? choiceProblem(rows) : null;
  const problemText =
    namePb === "empty"
      ? t("labels.err.emptyName")
      : namePb === "long"
        ? t("labels.err.longName")
        : choicePb === "empty"
          ? t("labels.err.emptyChoice")
          : choicePb === "duplicate"
            ? t("labels.err.duplicateChoice")
            : choicePb === "limit"
              ? t("labels.err.choiceLimit")
              : null;
  const problem = problemText;

  const submit = () =>
    once(async () => {
      if (problem !== null) return;
      const stale = sessionChanged();
      setBusy(true);
      setError(null);
      try {
        const choices = choosing ? rows.map((r) => ({ id: r.id, name: r.name.trim(), archived: r.archived })) : undefined;
        if (editing) {
          const body: LabelFieldPatch = { name: name.trim(), restricted };
          if (type !== field.type) body.type = type;
          if (choices) body.choices = choices;
          await onsave(body);
        } else {
          await onsave({ name: name.trim(), type, restricted, choices: choices?.map((c) => ({ name: c.name })) });
        }
      } catch (err) {
        if (stale()) return;
        const reason = labelFailure(err);
        setError(reason !== null ? t(`labels.err.${reason}`) : failureText(err, lang));
        setBusy(false);
      }
    });

  return (
    <Modal
      open
      title={editing ? t("labels.editTitle", { name: field.name }) : t("labels.createTitle")}
      onCancel={busy ? undefined : onclose}
      maskClosable={false}
      footer={null}
      destroyOnClose
    >
      <form
        id={id}
        noValidate
        style={{ display: "grid", gap: 16 }}
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <div style={{ display: "grid", gap: 4 }}>
          <label htmlFor={`${id}-name`}>{t("labels.nameLabel")}</label>
          <Input id={`${id}-name`} autoComplete="off" maxLength={255} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </div>

        <div style={{ display: "grid", gap: 4 }}>
          <label htmlFor={`${id}-type`}>{t("labels.typeLabel")}</label>
          <Select
            id={`${id}-type`}
            value={type}
            disabled={locked || busy}
            onChange={(v: LabelType) => setType(v)}
            options={LABEL_TYPES.map((v) => ({ value: v, label: t(`labels.type.${v}`) }))}
            style={{ maxWidth: 320 }}
          />
          {locked && (
            <p style={{ margin: 0, fontSize: 13 }}>{t("labels.typeLocked")}</p>
          )}
        </div>

        <Checkbox checked={restricted} disabled={busy} onChange={(e) => setRestricted(e.target.checked)}>
          {t("labels.restrictedLabel")}
        </Checkbox>
        {field !== null && field.restricted && !restricted && (
          <Alert type="warning" showIcon role="status" message={t("labels.restrictedOff", { count: field.values ?? 0 })} />
        )}

        {choosing && (
          <fieldset style={{ display: "grid", gap: 8, border: 0, padding: 0, margin: 0 }}>
            <legend style={{ padding: 0 }}>{t("labels.choices")}</legend>
            {rows.map((r, i) => (
              <div key={r.id ?? `new-${i}`} style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
                <Input
                  aria-label={t("labels.choiceName")}
                  maxLength={255}
                  value={r.name}
                  disabled={busy}
                  style={{ flex: "1 1 180px" }}
                  onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
                />
                {r.id !== undefined && (
                  <Button
                    size="small"
                    disabled={busy}
                    aria-pressed={r.archived}
                    onClick={() => setRows(rows.map((x, j) => (j === i ? { ...x, archived: !x.archived } : x)))}
                  >
                    {r.archived ? t("labels.restore") : t("labels.archive")}
                  </Button>
                )}
              </div>
            ))}
            <div>
              <Button disabled={busy} onClick={() => setRows([...rows, { name: "", archived: false }])}>
                {t("labels.addChoice")}
              </Button>
            </div>
          </fieldset>
        )}

        {problemText !== null && <Alert type="info" showIcon role="status" message={problemText} />}
        {error !== null && <Alert type="error" showIcon role="alert" message={error} />}

        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" }}>
          <Button disabled={busy} onClick={onclose}>
            {t("common.cancel")}
          </Button>
          <Button type="primary" htmlType="submit" loading={busy} disabled={busy || problem !== null}>
            {editing ? t("labels.save") : t("labels.create")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
