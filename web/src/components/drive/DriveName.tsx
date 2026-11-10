// SPDX-License-Identifier: AGPL-3.0-or-later

// The name cell of a drive list (tl4, decision 137): the item's name as a link to its address (or to a shortcut's target, when the
// caller reaches it), its kind marks, and any second line the page gives (a search result's snippet). The name is set in a bdi, so
// a name written right to left keeps its own order. The link keeps the office editor's rule for a plain click (oe1).
import type { ReactNode } from "react";
import { theme } from "antd";
import KindMarks from "../files/KindMarks";
import { openShortcut } from "../../lib/files/shortcut";
import { officeEdit, officeLinkClick } from "../../lib/files/editor";
import { go, location as routerLocation } from "../../lib/router";
import { get, useStore } from "../../lib/store";
import type { Node } from "../../lib/files/types";

// drivePath is the address an item opens at: its own, or its shortcut's target when the caller reaches the target. null when
// nothing opens (a shortcut to a place the caller cannot reach).
export function drivePath(n: Node): string | null {
  const opened = n.kind === "shortcut" ? openShortcut(n) : null;
  if (opened?.kind === "unavailable") return null;
  return opened ? opened.to : `/n/${n.id}`;
}

// openDrive opens an item in this tab: Enter on its row, or its ⋮ menu's Open.
export function openDrive(n: Node): void {
  const to = drivePath(n);
  if (to !== null) go(to);
}

export default function DriveName({ node, children }: { node: Node; children?: ReactNode }) {
  const { token } = theme.useToken();
  const editorOn = useStore(officeEdit);
  const to = drivePath(node);
  const name = <bdi>{node.name}</bdi>;
  return (
    <span style={{ display: "block", minWidth: 0 }}>
      <span style={{ display: "block", overflowWrap: "anywhere" }}>
        {to === null ? (
          <span style={{ fontWeight: 600 }}>{name}</span>
        ) : (
          <a
            href={to}
            onClick={(e) => officeLinkClick(e, node, editorOn, get(routerLocation).path + get(routerLocation).search, go)}
            style={{ fontWeight: 600, color: token.colorLink }}
          >
            {name}
          </a>
        )}
        <KindMarks node={node} />
      </span>
      {children}
    </span>
  );
}
