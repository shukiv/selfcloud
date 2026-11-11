// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Badge, Button, Card } from "antd";
import type { ReactNode } from "react";
import { useT } from "../../../lib/i18n";
import type { Setting } from "../../../lib/settings/api";
import { fieldKeys, type SectionSpec } from "../../../lib/settings/fields";
import type { Draft, Edits } from "../../../lib/settings/form";
import FieldRow from "./FieldRow";

// Notice is what the last Save of a section said: that it was saved, or why not.
export type Notice = { kind: "saved" } | { kind: "error"; text: string } | { kind: "conflict"; keys: string[] };

interface Props {
  section: SectionSpec;
  settings: Setting[];
  edits: Edits;
  errors: Record<string, string>;
  notice: Notice | null;
  dirty: boolean;
  busy: boolean;
  wide: boolean;
  onChange: (key: string, value: Draft) => void;
  onReset: (key: string) => void;
  onSave: () => void;
  onDiscard: () => void;
}

// CardFrame is the card every section sits in: its title and its one-line introduction. The card is a region named by its title,
// so a person can reach it from the section list, and an assistive technology lists it.
export function CardFrame({ id, title, intro, children }: { id: string; title: string; intro: string; children: ReactNode }) {
  return (
    <section aria-labelledby={`settings-${id}-title`} id={`settings-${id}`} style={{ minWidth: 0 }}>
      <Card
        title={
          <div>
            <h2 id={`settings-${id}-title`} style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
              {title}
            </h2>
            <div style={{ fontWeight: 400, fontSize: 13, whiteSpace: "normal", marginBlockStart: 2, color: "var(--ant-color-text-secondary)" }}>{intro}</div>
          </div>
        }
        styles={{ header: { paddingBlock: 12 } }}
      >
        {children}
      </Card>
    </section>
  );
}

// SectionCard is one section of fields with its own Save. Save sends this section's changed keys only.
export default function SectionCard({ section, settings, edits, errors, notice, dirty, busy, wide, onChange, onReset, onSave, onDiscard }: Props) {
  const { t } = useT();
  const byKey = new Map(settings.map((s) => [s.key, s]));
  const keys = new Set(fieldKeys(section));
  return (
    <CardFrame id={section.id} title={t(`settings.section.${section.id}`)} intro={t(`settings.section.${section.id}.intro`)}>
      {section.groups.map((g, i) => (
        <div key={g.title ?? i} style={{ marginBlockEnd: 8 }}>
          {g.title !== null && <h3 style={{ margin: i === 0 ? "0 0 4px" : "16px 0 4px", fontSize: 14, fontWeight: 600 }}>{t(g.title)}</h3>}
          {g.fields.map((f) => {
            const s = byKey.get(f.key);
            // A setting this server does not know is left out: the page draws what the server answers.
            if (s === undefined || !keys.has(f.key)) return null;
            return (
              <FieldRow
                key={f.key}
                spec={f}
                setting={s}
                edit={edits[f.key]}
                error={errors[f.key] ?? null}
                wide={wide}
                onChange={(v) => onChange(f.key, v)}
                onReset={() => onReset(f.key)}
              />
            );
          })}
        </div>
      ))}
      {notice?.kind === "error" && <Alert type="error" showIcon role="alert" message={notice.text} style={{ marginBlockEnd: 12 }} />}
      {notice?.kind === "conflict" && (
        <Alert type="warning" showIcon role="alert" message={t("settings.conflict", { keys: notice.keys.join(", ") })} style={{ marginBlockEnd: 12 }} />
      )}
      <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", flexWrap: "wrap", gap: 8, paddingBlockStart: 14, borderBlockStart: "1px solid var(--ant-color-split)" }}>
        {dirty && (
          <span style={{ marginInlineEnd: "auto", fontSize: 13 }}>
            <Badge color="var(--ant-color-primary)" text={t("settings.unsaved")} />
          </span>
        )}
        {notice?.kind === "saved" && !dirty && (
          <span role="status" style={{ marginInlineEnd: "auto", fontSize: 13, color: "var(--ant-color-success)" }}>
            {t("settings.saved")}
          </span>
        )}
        <Button onClick={onDiscard} disabled={!dirty || busy}>
          {t("settings.discard")}
        </Button>
        <Button type="primary" onClick={onSave} disabled={!dirty || busy} aria-busy={busy}>
          {t("settings.save")}
        </Button>
      </div>
    </CardFrame>
  );
}
