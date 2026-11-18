// SPDX-License-Identifier: AGPL-3.0-or-later

import { useRef, useState, type Key, type ReactNode } from "react";
import { Alert, Button, Modal, Tree, type TreeDataNode, type TreeProps } from "antd";
import { File, Film, Music } from "lucide-react";
import { childrenPage } from "../../lib/files/api";
import { fileErrorText } from "../../lib/files/fileErrors";
import { kindOf } from "../../lib/files/kind";
import type { FolderAccess, Node } from "../../lib/files/types";
import { useLang, useT } from "../../lib/i18n";
import { sessionChanged } from "../../lib/session";
import EmptyState from "../EmptyState";

// Where a tree node stands: a space (dir null) or a directory in one. Kept by key, so the tree can be
// rebuilt without losing a cursor.
interface Place {
  folder: string;
  dir: string | null;
  personal: boolean;
  cursor?: string;
  loaded: boolean;
}

const SPACE = (id: string) => `s:${id}`;
const DIR = (folder: string, dir: string) => `d:${folder}:${dir}`;
const FILE = (folder: string, id: string) => `f:${folder}:${id}`;

// PickFiles chooses files from the spaces, lazily, as the tree opens (slice pk1). It starts at the list of spaces;
// startAt (a space's ID) is opened and read at once. A place shows its directories first, then the files the caller
// accepts; other kinds are hidden. Only file leaves can be chosen: a directory or a space never can. In the single
// mode one file is chosen and the action names it; in the multiple mode files carry checkboxes, the order of checking
// is kept, the count shows in the action, and at most max files can be chosen. "Load more" reads the next page of the
// opened place. An answer that arrives after the session changed is dropped quietly, and no read is sent once it has.
export default function PickFiles({
  title,
  action,
  spaces,
  startAt,
  accept,
  multiple,
  max,
  onPick,
  onClose,
}: {
  title: string;
  action: string;
  spaces: FolderAccess[];
  startAt?: string;
  accept: (n: Node) => boolean;
  multiple?: boolean;
  max?: number;
  onPick: (nodes: Node[]) => void;
  onClose: () => void;
}): JSX.Element {
  const { t } = useT();
  const lang = useLang();
  const cap = max ?? 500;
  // The places of the spaces are known from the start; their children come with the first open.
  const places = useRef<Map<string, Place>>(new Map());
  if (places.current.size === 0) {
    for (const s of spaces) {
      places.current.set(SPACE(s.id), { folder: s.id, dir: null, personal: s.kind === "personal", loaded: false });
    }
  }
  // The file leaves, kept by key so a choice hands back the node the page read.
  const files = useRef<Map<string, Node>>(new Map());
  const [treeData, setTreeData] = useState<TreeDataNode[]>(() =>
    spaces.map((s) => ({ key: SPACE(s.id), title: s.name, isLeaf: false, checkable: false })),
  );
  // active is the opened place "Load more" and the empty note speak of; pick is the single choice, chosen the
  // multiple one in the order it was checked.
  const [active, setActive] = useState<string | null>(startAt ? SPACE(startAt) : null);
  const [pick, setPick] = useState<string | null>(null);
  const [chosen, setChosen] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [, bump] = useState(0);
  const stale = useRef(sessionChanged()).current;

  const place = active !== null ? places.current.get(active) : undefined;
  const count = multiple ? chosen.length : pick !== null ? 1 : 0;
  const over = multiple === true && count > cap;
  const canSubmit = multiple ? count > 0 && count <= cap : pick !== null;
  const submitLabel = multiple ? t("files.pick.submitCount", { action, count }) : action;

  // loadChildren reads one page of a place: its directories, then the files the caller accepts. A reset replaces the
  // children, a later page adds to them. A read is not sent once the session has changed, and an answer that arrives
  // after it has is dropped.
  async function loadChildren(key: string, reset: boolean) {
    const from = places.current.get(key);
    if (!from || stale()) return;
    setBusy(true);
    try {
      const page = await childrenPage(from.folder, from.dir, reset ? undefined : from.cursor);
      if (stale()) return;
      const dirs = page.items.filter((n) => n.kind === "dir");
      const kept = page.items.filter((n) => n.kind === "file" && accept(n));
      for (const n of dirs) {
        const k = DIR(from.folder, n.id);
        if (!places.current.has(k)) {
          places.current.set(k, { folder: from.folder, dir: n.id, personal: false, loaded: false });
        }
      }
      const fresh: TreeDataNode[] = [
        ...dirs.map((n) => ({ key: DIR(from.folder, n.id), title: n.name, isLeaf: false, checkable: false })),
        ...kept.map((n) => {
          const k = FILE(from.folder, n.id);
          files.current.set(k, n);
          return { key: k, title: n.name, isLeaf: true, checkable: true };
        }),
      ];
      places.current.set(key, { ...from, cursor: page.next_cursor, loaded: true });
      setTreeData((prev) => addChildren(prev, key, fresh, reset));
      setError(null);
    } catch (err) {
      if (stale()) return;
      setError(fileErrorText(err, lang));
    } finally {
      if (!stale()) {
        setBusy(false);
        bump((n) => n + 1);
      }
    }
  }

  // onCheck keeps the order of checking: the files still checked stay in their order, the newly checked go after them.
  const modeProps: TreeProps = multiple
    ? {
        checkable: true,
        checkStrictly: true,
        checkedKeys: chosen,
        onCheck: (checked) => {
          const now = (Array.isArray(checked) ? checked : checked.checked).map(String);
          const before = new Set(chosen);
          const nowSet = new Set(now);
          setChosen([...chosen.filter((k) => nowSet.has(k)), ...now.filter((k) => !before.has(k))]);
        },
      }
    : {
        selectedKeys: pick === null ? [] : [pick],
        onSelect: (_keys: Key[], info) => {
          if (info.node.isLeaf) setPick(String(info.node.key));
        },
      };

  function submitPick() {
    if (!canSubmit) return;
    if (multiple) {
      onPick(chosen.map((k) => files.current.get(k)).filter((n): n is Node => n !== undefined));
    } else if (pick !== null) {
      const n = files.current.get(pick);
      if (n) onPick([n]);
    }
  }

  // labelOf writes a place's name (a personal space says "My files") or a file's name beside its kind's icon.
  function labelOf(node: TreeDataNode): ReactNode {
    const key = String(node.key);
    const name = typeof node.title === "string" ? node.title : "";
    const here = places.current.get(key);
    if (here) {
      return <bdi>{here.dir === null && here.personal ? t("files.pick.mine") : name}</bdi>;
    }
    const n = files.current.get(key);
    const kind = n ? kindOf(n) : "file";
    const Glyph = kind === "video" ? Film : kind === "audio" ? Music : File;
    return (
      <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
        <Glyph size={16} aria-hidden="true" focusable="false" />
        <bdi>{name}</bdi>
      </span>
    );
  }

  const hasMore = place !== undefined && place.loaded && place.cursor !== undefined;
  const isEmpty = place !== undefined && place.loaded && !hasChildren(treeData, active);

  return (
    <Modal
      open
      title={title}
      onCancel={onClose}
      maskClosable={false}
      destroyOnHidden
      width={560}
      data-testid="pick-files"
      footer={[
        over ? (
          <p key="max" role="note" style={{ margin: 0 }}>
            {t("files.pick.maxFiles", { max: cap })}
          </p>
        ) : null,
        <Button key="cancel" onClick={onClose}>
          {t("common.cancel")}
        </Button>,
        <Button key="submit" type="primary" disabled={!canSubmit} onClick={submitPick}>
          {submitLabel}
        </Button>,
      ]}
    >
      {error && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}
      {spaces.length === 0 ? (
        <EmptyState size="compact" text={t("files.pick.noSpaces")} />
      ) : (
        <>
          <Tree
            treeData={treeData}
            // rc-tree reads a default-expanded node that has no children yet, so startAt is read at once on mount.
            defaultExpandedKeys={startAt ? [SPACE(startAt)] : []}
            onExpand={(_keys: Key[], info) => {
              if (info.expanded) setActive(String(info.node.key));
            }}
            loadData={(node) => loadChildren(String(node.key), true)}
            titleRender={(node) => labelOf(node)}
            blockNode
            {...modeProps}
          />
          {isEmpty && <EmptyState size="compact" text={t("files.pick.noMatch")} />}
          {hasMore && (
            <Button block disabled={busy} onClick={() => void loadChildren(active as string, false)}>
              {t("files.loadMore")}
            </Button>
          )}
        </>
      )}
    </Modal>
  );
}

// addChildren puts a page of children beneath the node with key: a reset replaces them, a later page is added after them.
function addChildren(tree: TreeDataNode[], key: string, fresh: TreeDataNode[], reset: boolean): TreeDataNode[] {
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

// hasChildren says whether the node with key has any child under it in the tree.
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
