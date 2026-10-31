// SPDX-License-Identifier: AGPL-3.0-or-later

import { Tag } from "antd";
import { useT } from "../../lib/i18n";
import { isColour } from "../../lib/files/palette";
import type { Node } from "../../lib/files/types";

// KindMarks says, in text, what a row is beyond its name: a shortcut, a shortcut whose target is out of reach, and a
// folder's colour. The icons carry the same things without words, so every list that shows a node uses these marks.
// A colour that is not on the palette is not named.
export default function KindMarks({ node }: { node: Node }) {
  const { t } = useT();
  const colour = node.kind === "dir" && isColour(node.color) ? node.color : null;
  return (
    <>
      {node.kind === "shortcut" && <span className="sr-only">{t("shortcut.kind")}</span>}
      {node.kind === "shortcut" && node.unavailable && (
        <Tag style={{ marginInlineStart: 4, marginInlineEnd: 0 }}>{t("shortcut.unavailable")}</Tag>
      )}
      {colour && <span className="sr-only">{t("files.colour.label", { colour: t(`colour.${colour}`) })}</span>}
    </>
  );
}
