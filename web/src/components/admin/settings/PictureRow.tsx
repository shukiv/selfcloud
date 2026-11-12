// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Tag, Tooltip } from "antd";
import { Lock, Trash2, Upload } from "lucide-react";
import { useRef, useState } from "react";
import { ApiError } from "../../../lib/apiError";
import { useLang, useT } from "../../../lib/i18n";
import { failureText } from "../../../lib/manage/failure";
import { deletePicture, putPicture, type Picture, type PictureKind, type SettingsAnswer } from "../../../lib/settings/api";

// The server's own limit, which the card states ahead of sending as a hint only: the server decides and its refusal is shown.
const MAX_BYTES = 256 * 1024;
const TYPES = ["image/png", "image/svg+xml"];

// typeOf is the type a file is sent as: the type the browser gives, or for a file it gives none (an SVG often has none) the type its
// name says.
function typeOf(file: File): string {
  if (file.type !== "") return file.type;
  const name = file.name.toLowerCase();
  return name.endsWith(".svg") ? "image/svg+xml" : name.endsWith(".png") ? "image/png" : "";
}

interface Props {
  kind: PictureKind;
  picture: Picture;
  wide: boolean;
  // onAnswer is given the settings answer of a change that was made.
  onAnswer: (answer: SettingsAnswer) => void;
  // onLocked is called when the server says the picture is set in the config file, so the page reads the settings again.
  onLocked: () => void;
}

// src is where a picture in use is read from, with its hash so a new picture is fetched again.
export function pictureSrc(kind: PictureKind, p: Picture): string | null {
  if (p.state !== "uploaded" && p.state !== "file") return null;
  const v = p.sha256 ?? "";
  return kind === "logo" ? `/brand/logo?v=${v}` : `/brand/icon-512.png?v=${v}`;
}

// PictureRow is the logo or the icon: what is in use, Upload (or Replace) and Remove as icon buttons with a tooltip. A change is made
// at once, as the API does, and is not part of the section's Save. A picture the config file sets is read-only. One that was stored
// and not used says why and offers Remove.
export default function PictureRow({ kind, picture, wide, onAnswer, onLocked }: Props) {
  const { t } = useT();
  const lang = useLang();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const used = picture.state === "uploaded";
  const src = pictureSrc(kind, picture);
  const upload = t(used ? `settings.picture.replace.${kind}` : `settings.picture.upload.${kind}`);
  const remove = t(`settings.picture.remove.${kind}`);

  const run = async (change: () => Promise<SettingsAnswer>) => {
    setBusy(true);
    setProblem(null);
    try {
      onAnswer(await change());
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && e.details.reason === "setting_locked") onLocked();
      // The server's refusal says why a picture was not taken (its size, its shape), so it is shown as it comes.
      setProblem(e instanceof ApiError && e.serverMessage !== "" ? e.serverMessage : failureText(e, lang));
    } finally {
      setBusy(false);
    }
  };

  const choose = (file: File | undefined) => {
    if (input.current) input.current.value = "";
    if (file === undefined) return;
    const type = typeOf(file);
    if (!TYPES.includes(type)) return setProblem(t("settings.picture.type"));
    if (file.size > MAX_BYTES) return setProblem(t("settings.picture.size", { size: Math.ceil(file.size / 1024), max: MAX_BYTES / 1024 }));
    void run(() => putPicture(kind, file, type));
  };

  return (
    <div
      data-picture={picture.key}
      style={{
        display: "grid",
        gridTemplateColumns: wide ? "minmax(0, 1fr) minmax(0, 340px)" : "minmax(0, 1fr)",
        gap: wide ? "8px 24px" : 8,
        padding: "14px 0",
        borderBlockStart: "1px solid var(--ant-color-split)",
        alignItems: "start",
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 8, fontWeight: 500 }}>
          <span>{t(`settings.picture.${kind}`)}</span>
          {picture.locked ? (
            <Tag icon={<Lock size={12} aria-hidden="true" style={{ marginInlineEnd: 4, verticalAlign: "-1px" }} />} style={{ marginInlineEnd: 0 }}>
              {t("settings.locked")}
            </Tag>
          ) : used ? (
            <Tag color="blue" style={{ marginInlineEnd: 0 }}>
              {t("settings.source.settings")}
            </Tag>
          ) : (
            <Tag style={{ marginInlineEnd: 0 }}>{t("settings.source.default")}</Tag>
          )}
        </div>
        <p style={{ margin: "4px 0 0", fontSize: 13, lineHeight: 1.5, color: "var(--ant-color-text-secondary)", maxWidth: "62ch" }}>{t(`settings.picture.${kind}.help`)}</p>
      </div>
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, justifyContent: wide ? "flex-end" : "flex-start", flexWrap: "wrap" }}>
          {src !== null && <img src={src} alt="" height={kind === "logo" ? 28 : 32} style={{ maxWidth: 120, background: "var(--ant-color-fill-quaternary)", borderRadius: 4 }} />}
          {!picture.locked && (
            <>
              <input
                ref={input}
                type="file"
                hidden
                accept=".png,.svg,image/png,image/svg+xml"
                aria-label={t(`settings.picture.file.${kind}`)}
                onChange={(e) => choose(e.target.files?.[0])}
              />
              <Tooltip title={upload}>
                <Button aria-label={upload} disabled={busy} icon={<Upload size={16} aria-hidden="true" />} onClick={() => input.current?.click()} />
              </Tooltip>
              {(used || picture.state === "unusable") && (
                <Tooltip title={remove}>
                  <Button danger aria-label={remove} disabled={busy} icon={<Trash2 size={16} aria-hidden="true" />} onClick={() => void run(() => deletePicture(kind))} />
                </Tooltip>
              )}
            </>
          )}
        </div>
        {picture.state === "unusable" && (
          <Alert type="warning" showIcon style={{ marginBlockStart: 8 }} message={t("settings.picture.unusable", { reason: picture.reason ?? "" })} />
        )}
        {problem !== null && (
          <div role="alert" style={{ marginBlockStart: 6, color: "var(--ant-color-error)", fontSize: 13, textAlign: wide ? "end" : "start" }}>
            {problem}
          </div>
        )}
      </div>
    </div>
  );
}
