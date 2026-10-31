// SPDX-License-Identifier: AGPL-3.0-or-later

import { Tag, theme as antdTheme } from "antd";
import { CirclePlay } from "lucide-react";
import { useT } from "../../lib/i18n";
import { isColour } from "../../lib/files/palette";
import { kindOf } from "../../lib/files/kind";
import type { Node } from "../../lib/files/types";

// KindMarks says, in text, what a row is beyond its name: a shortcut, a shortcut whose target is out of reach, a
// folder's colour, and a video or an audio file, which can be played. The icons carry the same things without words,
// so every list that shows a node uses these marks. A colour that is not on the palette is not named.
export default function KindMarks({ node }: { node: Node }) {
  const { t } = useT();
  const colour = node.kind === "dir" && isColour(node.color) ? node.color : null;
  const kind = kindOf(node);
  const playable = kind === "video" || kind === "audio" ? kind : null;
  return (
    <>
      {node.kind === "shortcut" && <span className="sr-only">{t("shortcut.kind")}</span>}
      {node.kind === "shortcut" && node.unavailable && (
        <Tag style={{ marginInlineStart: 4, marginInlineEnd: 0 }}>{t("shortcut.unavailable")}</Tag>
      )}
      {colour && <span className="sr-only">{t("files.colour.label", { colour: t(`colour.${colour}`) })}</span>}
      {playable && <PlayMark kind={playable} />}
    </>
  );
}

// PlayMark is the small triangle a video or an audio file shows, so a person sees it can be played without selecting it.
// Its words are read by a screen reader: "Video" or "Audio".
export function PlayMark({ kind }: { kind: "video" | "audio" }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  return (
    <span
      data-testid="play-mark"
      data-kind={kind}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 2,
        marginInlineStart: 6,
        padding: "0 6px 0 4px",
        verticalAlign: "middle",
        fontSize: token.fontSizeSM,
        color: token.colorPrimary,
        background: token.colorPrimaryBg,
        borderRadius: 999,
        lineHeight: "18px",
        whiteSpace: "nowrap",
      }}
    >
      <CirclePlay size={14} aria-hidden="true" focusable="false" />
      <span className="sr-only">{kind === "video" ? t("files.mark.video") : t("files.mark.audio")}</span>
    </span>
  );
}
