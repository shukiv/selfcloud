// SPDX-License-Identifier: AGPL-3.0-or-later

import { useRef, useState, type ReactNode, type Key } from "react";
import { Alert, Button, Modal, Tree, type TreeDataNode } from "antd";
import { childrenPage } from "../../lib/files/api";
import { fileErrorText } from "../../lib/files/fileErrors";
import { atLeast } from "../../lib/files/roles";
import type { FolderAccess, Node, Role } from "../../lib/files/types";
import { useLang, useT } from "../../lib/i18n";

// A destination: a folder, and a directory in it (null for the folder's root).
export interface Destination {
  folder: string;
  dir: string | null;
}

// Where a tree node stands: a space (dir null) or a directory in one. Kept by key, so the tree can be
// rebuilt without losing a cursor.
interface Place {
  folder: string;
  dir: string | null;
  role: Role;
  personal: boolean;
  cursor?: string;
  loaded: boolean;
}

const SPACE = (id: string) => `s:${id}`;
const DIR = (folder: string, dir: string) => `d:${folder}:${dir}`;

// PickDestination chooses where to move or copy. It starts at the list of spaces and goes down through folders
// and directories, lazily, as the tree opens. Places the caller can only view are shown and cannot be chosen:
// the submit button stays off for them. Only directories are listed. "Load more" reads the next page of the
// selected place's directories.
export default function PickDestination({
  mode,
  spaces,
  onPick,
  onClose,
}: {
  mode: "move" | "copy" | "shortcut";
  spaces: FolderAccess[];
  onPick: (dest: Destination) => void;
  onClose: () => void;
}) {
  const { t } = useT();
  const lang = useLang();
  // The places of the spaces are known from the start; their children come with the first open.
  const places = useRef<Map<string, Place>>(new Map());
  if (places.current.size === 0) {
    for (const s of spaces) {
      places.current.set(SPACE(s.id), { folder: s.id, dir: null, role: s.role, personal: s.kind === "personal", loaded: false });
    }
  }
  const [treeData, setTreeData] = useState<TreeDataNode[]>(() => spaces.map((s) => ({ key: SPACE(s.id), title: s.name, isLeaf: false })));
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [, bump] = useState(0);

  const place = selected !== null ? places.current.get(selected) : undefined;
  // A shortcut goes in a directory, so a space's root cannot be chosen for it.
  const canPick = place !== undefined && atLeast(place.role, "contributor") && (mode !== "shortcut" || place.dir !== null);
  const title = mode === "move" ? t("files.pick.move") : mode === "shortcut" ? t("files.pick.shortcut") : t("files.pick.copy");
  const submit = mode === "move" ? t("files.pick.submitMove") : mode === "shortcut" ? t("files.pick.submitShortcut") : t("files.pick.submitCopy");

  // loadChildren reads one page of a place's directories. A reset replaces the children, a later page adds to them.
  async function loadChildren(key: string, reset: boolean) {
    const from = places.current.get(key);
    if (!from) return;
    setBusy(true);
    try {
      const page = await childrenPage(from.folder, from.dir, reset ? undefined : from.cursor);
      const dirs = page.items.filter((n) => n.kind === "dir");
      for (const n of dirs) {
        const k = DIR(from.folder, n.id);
        if (!places.current.has(k)) {
          places.current.set(k, { folder: from.folder, dir: n.id, role: n.role ?? "viewer", personal: false, loaded: false });
        }
      }
      places.current.set(key, { ...from, cursor: page.next_cursor, loaded: true });
      setTreeData((prev) => addChildren(prev, key, dirs, from.folder, reset));
      setError(null);
    } catch (err) {
      setError(fileErrorText(err, lang));
    } finally {
      setBusy(false);
      bump((n) => n + 1);
    }
  }

  const hasMore = place !== undefined && place.loaded && place.cursor !== undefined;
  const isEmpty = place !== undefined && place.loaded && place.dir !== undefined && !hasChildren(treeData, selected);

  return (
    <Modal
      open
      title={title}
      onCancel={onClose}
      maskClosable={false}
      destroyOnHidden
      width={560}
      data-testid="pick"
      footer={[
        <Button key="cancel" onClick={onClose}>
          {t("common.cancel")}
        </Button>,
        <Button
          key="submit"
          type="primary"
          disabled={!canPick}
          onClick={() => place && onPick({ folder: place.folder, dir: place.dir })}
        >
          {submit}
        </Button>,
      ]}
    >
      {error && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}
      {spaces.length === 0 ? (
        <p>{t("files.pick.noSpaces")}</p>
      ) : (
        <>
          <p>{selected === null ? t("files.pick.spaces") : t("files.pick.folders")}</p>
          <Tree
            treeData={treeData}
            selectedKeys={selected === null ? [] : [selected]}
            onSelect={(keys: Key[]) => setSelected(keys.length ? String(keys[0]) : null)}
            loadData={(node) => loadChildren(String(node.key), true)}
            titleRender={(node) => labelOf(node, places.current, t)}
            blockNode
          />
          {selected !== null && !canPick && <p role="note">{t("files.pick.viewOnly")}</p>}
          {isEmpty && <p>{t("files.pick.empty")}</p>}
          {hasMore && (
            <Button block disabled={busy} onClick={() => void loadChildren(selected as string, false)}>
              {t("files.loadMore")}
            </Button>
          )}
        </>
      )}
    </Modal>
  );
}

// addChildren puts a page of directories beneath the node with key: a reset replaces its children, a later
// page is added after them.
function addChildren(tree: TreeDataNode[], key: string, dirs: Node[], folder: string, reset: boolean): TreeDataNode[] {
  const fresh: TreeDataNode[] = dirs.map((n) => ({ key: DIR(folder, n.id), title: n.name, isLeaf: false }));
  const walk = (list: TreeDataNode[]): TreeDataNode[] =>
    list.map((n) => {
      if (n.key === key) {
        const before = reset ? [] : (n.children ?? []);
        return { ...n, children: [...before, ...fresh] };
      }
      return n.children ? { ...n, children: walk(n.children) } : n;
    });
  return walk(tree);
}

// hasChildren says whether the node with key has any directory under it in the tree.
function hasChildren(tree: TreeDataNode[], key: string | null): boolean {
  if (key === null) return true;
  const find = (list: TreeDataNode[]): TreeDataNode | undefined => {
    for (const n of list) {
      if (n.key === key) return n;
      const hit = n.children ? find(n.children) : undefined;
      if (hit) return hit;
    }
    return undefined;
  };
  return (find(tree)?.children?.length ?? 0) > 0;
}

// labelOf writes a node's name and, beside it, the role the caller has there, as the Svelte list does. A
// personal space says "My files" in place of a role.
function labelOf(node: TreeDataNode, places: Map<string, Place>, t: (k: string) => string): ReactNode {
  const place = places.get(String(node.key));
  const name = typeof node.title === "string" ? node.title : "";
  if (!place) return <bdi>{name}</bdi>;
  let note: string;
  if (place.dir === null && place.personal) note = t("files.pick.mine");
  else if (place.dir === null || atLeast(place.role, "contributor")) note = t(`role.${place.role}`);
  else note = t("files.pick.viewOnlyShort");
  return (
    <span style={{ display: "inline-flex", gap: 12, justifyContent: "space-between", width: "100%" }}>
      <bdi>{name}</bdi>
      <span style={{ fontSize: 12, opacity: 0.75 }}>{note}</span>
    </span>
  );
}
