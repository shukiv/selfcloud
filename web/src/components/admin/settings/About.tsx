// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties, ReactNode } from "react";
import { useLang, useT } from "../../../lib/i18n";
import { formatSizeBytes } from "../../../lib/settings/format";
import type { Info } from "../../../lib/manage/types";
import type { Fact } from "../../../lib/settings/api";
import { CardFrame } from "./SectionCard";

// The public address and the web interface are in the installation's own facts above; the settings answer repeats them.
const SHOWN_ABOVE = new Set(["public_url", "ui.enabled"]);

const kv: CSSProperties = { display: "grid", gridTemplateColumns: "minmax(0, 220px) minmax(0, 1fr)", margin: 0 };
const cell: CSSProperties = { padding: "9px 0", borderBlockStart: "1px solid var(--ant-color-split)", minWidth: 0, overflowWrap: "anywhere", margin: 0 };
const mono: CSSProperties = { fontFamily: "ui-monospace, monospace", fontSize: 13 };

// About is the last section: the installation's facts, and the read-only values of the server's config file. Nothing here can be
// changed from the page.
export default function About({ info, facts, failure }: { info: Info | undefined; facts: Fact[]; failure: string | null }) {
  const { t } = useT();
  const lang = useLang();
  const row = (label: ReactNode, value: ReactNode, key: string) => (
    <div key={key} style={{ display: "contents" }}>
      <dt style={{ ...cell, color: "var(--ant-color-text-secondary)", paddingInlineEnd: 12 }}>{label}</dt>
      <dd style={cell}>{value}</dd>
    </div>
  );
  const value = (f: Fact): string => {
    if (typeof f.value === "boolean") return t(f.value ? "settings.on" : "settings.off");
    if (Array.isArray(f.value)) return f.value.length > 0 ? f.value.join(", ") : t("settings.none");
    return f.value === "" ? t("settings.none") : String(f.value);
  };
  return (
    <CardFrame id="about" title={t("settings.section.about")} intro={t("settings.section.about.intro")}>
      {failure !== null && <p style={{ margin: "0 0 12px", color: "var(--ant-color-error)" }}>{failure}</p>}
      <dl style={kv}>
        {info !== undefined && [
          row(t("settings.about.version"), <span style={mono}>{info.version}</span>, "version"),
          row(t("settings.about.publicUrl"), <bdi>{info.public_url}</bdi>, "publicUrl"),
          row(t("settings.about.webInterface"), t(info.web_interface ? "settings.on" : "settings.off"), "webInterface"),
          row(t("settings.about.pieceSize"), formatSizeBytes(info.limits.max_chunk_size, lang), "pieceSize"),
          row(t("settings.about.largestFile"), formatSizeBytes(info.limits.max_file_size, lang), "largestFile"),
        ]}
      </dl>
      <p style={{ display: "flex", flexWrap: "wrap", gap: 16, margin: "12px 0 0" }}>
        <a href="/api/v1/openapi.yaml">{t("settings.about.spec")}</a>
        <a href="/readyz">{t("settings.about.readiness")}</a>
      </p>
      {facts.some((f) => !SHOWN_ABOVE.has(f.key)) && (
        <>
          <h3 style={{ margin: "20px 0 4px", fontSize: 14, fontWeight: 600 }}>{t("settings.about.facts")}</h3>
          <dl style={kv}>
            {facts
              .filter((f) => !SHOWN_ABOVE.has(f.key))
              .map((f) => row(<code style={mono}>{f.key}</code>, <bdi>{value(f)}</bdi>, f.key))}
          </dl>
        </>
      )}
    </CardFrame>
  );
}
