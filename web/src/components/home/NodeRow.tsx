// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { theme } from "antd";
import Icon from "../files/Icon";
import KindMarks from "../files/KindMarks";
import { openShortcut } from "../../lib/files/shortcut";
import { kindOf } from "../../lib/files/kind";
import type { Node } from "../../lib/files/types";

// NodeRow is one item in a short list on a user page: its kind, its name (a link to its address), what happened to it,
// and an optional action. The name is set in a bdi, so a name written right to left keeps its own order.
export default function NodeRow({ node, meta, action }: { node: Node; meta: string; action?: ReactNode }) {
  const { token } = theme.useToken();
  // A shortcut opens its target when the caller reaches it, and is plain text when it does not: its own ID is never a link.
  const opened = node.kind === "shortcut" ? openShortcut(node) : null;
  const name = <bdi>{node.name}</bdi>;
  return (
    <li
      className="item"
      style={{
        display: "flex",
        flexWrap: "wrap",
        alignItems: "center",
        gap: "8px 12px",
        minBlockSize: 44,
        padding: "8px 12px",
        background: token.colorBgContainer,
        border: `1px solid ${token.colorBorderSecondary}`,
        borderRadius: token.borderRadius,
      }}
    >
      <Icon kind={kindOf(node)} size={24} />
      {opened?.kind === "unavailable" ? (
        <span style={{ flex: "1 1 160px", minInlineSize: 0, overflowWrap: "anywhere", fontWeight: 600 }}>{name}</span>
      ) : (
        <a
          href={opened ? opened.to : `/n/${node.id}`}
          style={{ flex: "1 1 160px", minInlineSize: 0, overflowWrap: "anywhere", fontWeight: 600, color: token.colorPrimary }}
        >
          {name}
        </a>
      )}
      <KindMarks node={node} />
      <span style={{ fontSize: token.fontSizeSM, color: token.colorTextSecondary }}>{meta}</span>
      {action}
    </li>
  );
}
