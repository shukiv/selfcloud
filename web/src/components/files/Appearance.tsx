// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type CSSProperties } from "react";
import { Alert, Button, Input, Radio } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { fileErrorText } from "../../lib/files/fileErrors";
import { updateAppearance } from "../../lib/files/api";
import { savedNode } from "../../lib/files/appearance";
import { COLOURS, colourVar, isColour } from "../../lib/files/palette";
import { can } from "../../lib/files/roles";
import type { Node } from "../../lib/files/types";
import { sessionChanged, signingOut } from "../../lib/session";

// DESCRIPTION_MAX is the server's limit on a description, in characters.
const DESCRIPTION_MAX = 1000;

// Appearance shows an item's colour and description. An editor sets them; a reader sees them as text. The server
// decides who may set them and checks the values; these controls only offer what it accepts. A description is plain
// text: it is shown as text, with its line breaks kept, and never as HTML.
export default function Appearance({ node, onChanged }: { node: Node; onChanged?: (next: Node) => void }) {
  const { t } = useT();
  const lang = useLang();
  const hasColour = node.kind === "dir";
  const canSet = can.edit(node.role) && node.kind !== "shortcut";
  const [colour, setColour] = useState<string>(node.color ?? "");
  const [text, setText] = useState(node.description ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  // mounted is false once the drawer has gone: a save answered then is not reported to the page, which may show
  // another item by now.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function save() {
    const changed = sessionChanged();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const body: { color?: string | null; description?: string | null } = {};
      if (hasColour) body.color = colour === "" ? null : colour;
      body.description = text === "" ? null : text;
      const next = await updateAppearance(node.id, body);
      if (changed() || !mounted.current) return;
      setSaved(true);
      onChanged?.(savedNode(node, next));
    } catch (err) {
      if (!changed() && !signingOut() && mounted.current) setError(fileErrorText(err, lang));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  if (!canSet) {
    const hasText = (node.description ?? "") !== "";
    const named = hasColour && isColour(node.color) ? node.color : null;
    if (!hasText && !named) return null;
    return (
      <section aria-label={t("files.appearance.heading")} style={{ marginBlockStart: 16, display: "grid", gap: 8 }}>
        {named && (
          <p style={{ margin: 0, display: "flex", alignItems: "center", gap: 8 }}>
            <span aria-hidden="true" style={swatch(named)} />
            <span>{t(`colour.${named}`)}</span>
          </p>
        )}
        {hasText && (
          <p data-testid="description" dir="auto" style={TEXT}>
            {node.description}
          </p>
        )}
      </section>
    );
  }

  return (
    <section aria-label={t("files.appearance.heading")} style={{ marginBlockStart: 16, display: "grid", gap: 12 }}>
      {hasColour && (
        <div style={{ display: "grid", gap: 8 }}>
          <span id="appearance-colour-label">{t("files.appearance.colour")}</span>
          <div role="radiogroup" aria-labelledby="appearance-colour-label">
            <Radio.Group
              value={colour}
              disabled={busy}
              onChange={(e) => {
                setColour(String(e.target.value));
                setSaved(false);
              }}
              style={{ display: "flex", flexWrap: "wrap", gap: 8 }}
            >
              <Radio value="">{t("files.appearance.noColour")}</Radio>
              {COLOURS.map((c) => (
                <Radio key={c} value={c}>
                  <span aria-hidden="true" style={swatch(c)} />
                  {t(`colour.${c}`)}
                </Radio>
              ))}
            </Radio.Group>
          </div>
        </div>
      )}
      <div style={{ display: "grid", gap: 8 }}>
        <label htmlFor="appearance-description">{t("files.appearance.description")}</label>
        <Input.TextArea
          id="appearance-description"
          rows={4}
          maxLength={DESCRIPTION_MAX}
          value={text}
          disabled={busy}
          onChange={(e) => {
            setText(e.target.value);
            setSaved(false);
          }}
          aria-describedby="appearance-description-hint"
        />
        <p id="appearance-description-hint" style={{ margin: 0, fontSize: 13 }}>
          {t("files.appearance.hint", { used: text.length, max: DESCRIPTION_MAX })}
        </p>
      </div>
      {error && <Alert type="error" showIcon role="alert" message={error} />}
      {saved && (
        <p role="status" style={{ margin: 0 }}>
          {t("files.appearance.saved")}
        </p>
      )}
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <Button type="primary" disabled={busy} onClick={() => void save()}>
          {t("files.appearance.save")}
        </Button>
      </div>
    </section>
  );
}

// swatch paints a palette name as a small dot, beside its text.
function swatch(c: string): CSSProperties {
  return {
    display: "inline-block",
    width: 12,
    height: 12,
    borderRadius: "50%",
    background: colourVar(c as (typeof COLOURS)[number]),
    flex: "none",
    marginInlineEnd: 6,
  };
}

const TEXT: CSSProperties = { margin: 0, whiteSpace: "pre-wrap", overflowWrap: "anywhere" };
