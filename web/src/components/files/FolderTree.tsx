// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../../lib/apiError";
import { Button, Skeleton, theme as antdTheme } from "antd";
import { ChevronDown, ChevronRight, Folder } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { queryKey } from "../../lib/query";
import { childrenPage } from "../../lib/files/api";
import { fileErrorText } from "../../lib/files/fileErrors";
import { loadDirs } from "../../lib/files/treeLoad";
import { revealed, withExpanded, type TreeState } from "../../lib/files/treeState";
import type { FolderAccess } from "../../lib/files/types";
import type { Sidebar } from "../../theme/tokens";

// The folder tree in the sidebar: the person's own space (My files) and the team spaces, and the folders in them, each read
// when it is first opened. It is a place to go (a click opens the folder in the listing) and a place to drop (an item dragged
// onto a folder moves into it): each folder carries the attributes the drag gesture reads (see useGestures), with the ids on
// the way down so a folder cannot be dropped into something below itself. It is a lazy chunk: the sidebar loads it.

export interface FolderTreeProps {
  spaces: FolderAccess[];
  // folder and chain say where the listing is: the space, and the directories below its root down to the one shown.
  folder: string;
  chain: string[];
  // marking says the place is on screen (the files pages): the folder is marked, and the way to it is opened. Elsewhere the
  // tree shows the spaces and keeps what is open, marking nothing.
  marking: boolean;
  state: TreeState;
  onChange: (next: TreeState) => void;
  onOpen: (folder: string, dir: string | null) => void;
  coarse: boolean;
  // top is the folder the person reaches the place by when the space's own root is not theirs (they were given a folder below
  // it): the tree starts there, in place of the space's root, and only shows what the person can reach.
  top?: { id: string; name: string; role: string | undefined };
  // colours are the sidebar's, when the tree sits in the sidebar: the text, and the yellow pill of the current folder.
  colours?: Sidebar;
  // spaceLabel and spaceEnd add to a space's own row: a tag after its name, and a control at its end (the team's options).
  spaceLabel?: (s: FolderAccess) => ReactNode;
  spaceEnd?: (s: FolderAccess) => ReactNode;
  // onMenu opens a space's options from the keyboard (Shift+F10 or the Menu key on its row).
  onMenu?: (s: FolderAccess) => void;
  // onGone is told when a space's own read answers not found or forbidden: the space is no longer the person's to see.
  onGone?: (s: FolderAccess) => void;
}

interface Ctx extends FolderTreeProps {
  currentKey: string | null;
  tabKey: string;
  setTabKey: (k: string) => void;
}

// Branch is one folder in the tree and, when it is open, the folders in it. root is set on a space's own row.
function Branch(p: { ctx: Ctx; space: string; id: string | null; name: string; role: string | undefined; level: number; above: string[]; root?: FolderAccess }) {
  const { ctx, space, id, name, role, level, above, root } = p;
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  const key = id ?? space;
  const open = ctx.state.expanded.includes(key);
  const path = [space, ...above, ...(id ? [id] : [])];
  // The folders inside are read the first time the folder is opened, and again when something changes (refreshTree).
  const q = useQuery({
    queryKey: queryKey("tree", space, id),
    queryFn: () => loadDirs((cursor) => childrenPage(space, id, cursor, { key: "name", dir: "asc" })),
    enabled: open,
  });
  // A space's root that the server says is not there for this person has nothing to show and cannot be retried into being.
  const unreachable = id === null && q.error instanceof ApiError && q.error.code === "not_found";
  // A space whose root answers not found or forbidden is no longer the person's: the caller is told once, to read the spaces again.
  const gone = id === null && q.error instanceof ApiError && (q.error.code === "not_found" || q.error.status === 403);
  useEffect(() => {
    if (gone && root) ctx.onGone?.(root);
    // Told once per answer: the callback is read through the context when the answer arrives.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gone]);
  const dirs = q.data?.dirs ?? [];
  // Until it has been read a folder may have folders in it; once read with none, it has no arrow.
  const hasKids = !unreachable && (q.data === undefined || dirs.length > 0 || q.data.capped);
  const current = ctx.currentKey === key;
  const side = ctx.colours;
  // On a finger the row, its arrow and its link are each 44 px; with a mouse the arrow is 24 and the link 28 in a row of 32.
  const size = ctx.coarse ? 44 : 32;
  const arrow = ctx.coarse ? 44 : 24;
  const link = ctx.coarse ? 44 : 28;
  const toggle = () => ctx.onChange(withExpanded(ctx.state, key, !open));
  const textColour = side ? side.text : undefined;
  return (
    <li
      role="treeitem"
      aria-level={level}
      aria-expanded={hasKids ? open : undefined}
      aria-selected={current}
      aria-current={current ? "location" : undefined}
      data-tree-key={key}
      // A row is named by its own folder: the name of the subtree under it would read every folder and arrow inside.
      aria-label={name}
      tabIndex={ctx.tabKey === key ? 0 : -1}
      onFocus={(e) => {
        if (e.target === e.currentTarget) ctx.setTabKey(key);
      }}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return;
        treeKey(e, { open, hasKids, toggle, go: () => ctx.onOpen(space, id), menu: root && ctx.onMenu ? () => ctx.onMenu?.(root) : undefined });
      }}
      style={{ listStyle: "none", outline: "none" }}
    >
      <div
        className={side ? "tree-row tree-row-side" : "tree-row"}
        data-drop-folder={space}
        data-drop-dir={id ?? ""}
        data-drop-role={role ?? ""}
        data-drop-name={name}
        data-drop-path={path.join(",")}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 2,
          minBlockSize: size,
          paddingInlineStart: (level - 1) * 14 + 2,
          borderRadius: token.borderRadiusSM,
          color: textColour,
          background: current ? (side ? side.active : token.colorPrimaryBg) : undefined,
          fontWeight: current ? 600 : undefined,
        }}
      >
        {hasKids ? (
          <Button
            type="text"
            size="small"
            tabIndex={-1}
            aria-label={open ? t("files.tree.collapse", { name }) : t("files.tree.expand", { name })}
            onClick={toggle}
            icon={open ? <ChevronDown size={14} aria-hidden="true" /> : <ChevronRight className="icon-dir" size={14} aria-hidden="true" />}
            style={{ flex: "none", minInlineSize: arrow, minBlockSize: arrow, padding: 0, color: textColour }}
          />
        ) : (
          <span aria-hidden="true" style={{ flex: "none", inlineSize: arrow }} />
        )}
        <a
          href={id ? `/files/${space}/${id}` : `/files/${space}`}
          tabIndex={-1}
          title={name}
          onClick={(e) => {
            if (e.metaKey || e.ctrlKey || e.shiftKey) return;
            e.preventDefault();
            ctx.onOpen(space, id);
          }}
          style={{ display: "flex", alignItems: "center", gap: 6, flex: "1 1 auto", minInlineSize: 0, minBlockSize: link, color: "inherit", textDecoration: "none" }}
        >
          <Folder size={16} aria-hidden="true" style={{ flex: "none" }} />
          <bdi style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minInlineSize: 0 }}>{name}</bdi>
          {root && ctx.spaceLabel?.(root)}
        </a>
        {root && ctx.spaceEnd?.(root)}
      </div>
      {open && (
        <ul role="group" style={{ margin: 0, padding: 0 }}>
          {q.isPending && (
            <li role="none" aria-busy="true" style={{ listStyle: "none", paddingInlineStart: level * 14 + 8 }}>
              <span className="sr-only">{t("files.loading")}</span>
              <Skeleton active title={false} paragraph={{ rows: 1, width: 80 }} />
            </li>
          )}
          {q.isError && !unreachable && (
            <li role="none" style={{ listStyle: "none", paddingInlineStart: level * 14 + 8 }}>
              <div role="alert" style={{ color: side ? side.text : token.colorError, fontSize: token.fontSizeSM }}>
                {t("files.tree.failed")} <bdi>{fileErrorText(q.error, lang)}</bdi>
              </div>
              <Button size="small" onClick={() => void q.refetch()} style={ctx.coarse ? { minBlockSize: 44 } : undefined}>
                {t("files.retry")}
              </Button>
            </li>
          )}
          {dirs.map((d) => (
            <Branch key={d.id} ctx={ctx} space={space} id={d.id} name={d.name} role={d.role} level={level + 1} above={[...above, ...(id ? [id] : [])]} />
          ))}
          {q.data?.capped && (
            <li role="none" style={{ listStyle: "none", paddingInlineStart: level * 14 + 8, color: side ? side.caption : token.colorTextSecondary, fontSize: token.fontSizeSM }}>
              {t("files.tree.capped")}
            </li>
          )}
        </ul>
      )}
    </li>
  );
}

// treeKey is the keyboard of one folder: Enter or Space opens it in the listing; Right opens the folder, then moves into it; Left
// closes it, then moves out. Up, Down, Home and End move between the folders shown; the tree decides where from the DOM.
function treeKey(e: KeyboardEvent<HTMLElement>, a: { open: boolean; hasKids: boolean; toggle: () => void; go: () => void; menu?: () => void }) {
  const rtl = document.documentElement.dir === "rtl";
  // The menu key and Shift+F10 open a space's options: the options are not a tab stop of their own (the tree is one).
  if (e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey)) {
    e.preventDefault();
    a.menu?.();
    return;
  }
  const items = () => Array.from(e.currentTarget.closest<HTMLElement>('[role="tree"]')?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? []);
  const focusAt = (to: number) => {
    const list = items();
    list[Math.max(0, Math.min(list.length - 1, to))]?.focus();
  };
  const index = items().indexOf(e.currentTarget);
  const forward = rtl ? "ArrowLeft" : "ArrowRight";
  const back = rtl ? "ArrowRight" : "ArrowLeft";
  switch (e.key) {
    case "Enter":
    case " ":
      e.preventDefault();
      a.go();
      return;
    case "ArrowDown":
      e.preventDefault();
      focusAt(index + 1);
      return;
    case "ArrowUp":
      e.preventDefault();
      focusAt(index - 1);
      return;
    case "Home":
      e.preventDefault();
      focusAt(0);
      return;
    case "End":
      e.preventDefault();
      focusAt(items().length - 1);
      return;
    case forward:
      e.preventDefault();
      if (a.hasKids && !a.open) a.toggle();
      else if (a.open) focusAt(index + 1);
      return;
    case back:
      e.preventDefault();
      if (a.open) a.toggle();
      else e.currentTarget.parentElement?.closest<HTMLElement>('[role="treeitem"]')?.focus();
      return;
  }
}

export default function FolderTree(props: FolderTreeProps) {
  const { t } = useT();
  const { spaces, folder, chain, marking, state, onChange, top } = props;
  // The folders on the way to the place shown are opened, so the tree shows where the person is. It happens when the place
  // changes, and not again when the person closes one of them.
  const ancestors = useMemo(() => (marking && folder ? [folder, ...chain] : []), [marking, folder, chain]);
  const ancestorsKey = ancestors.join(",");
  const stateRef = useRef(state);
  stateRef.current = state;
  useEffect(() => {
    if (ancestorsKey === "") return;
    const next = revealed(stateRef.current, ancestorsKey.split(","));
    if (next !== stateRef.current) onChange(next);
    // onChange writes the state; the effect reads it through the ref, so it runs for a new place only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ancestorsKey]);
  const currentKey = marking && folder ? (chain.length > 0 ? chain[chain.length - 1] : folder) : null;
  const [tabKey, setTabKey] = useState<string>(currentKey ?? spaces[0]?.id ?? "");
  useEffect(() => {
    if (currentKey !== null) setTabKey(currentKey);
  }, [currentKey]);
  // The tree is one tab stop, and it is always on a row that is rendered: the place's row when it is shown, else the first row.
  // It is checked after each change of the tree, including a branch that opens, closes or loses its rows (the observer).
  const navRef = useRef<HTMLElement | null>(null);
  const tabRef = useRef(tabKey);
  tabRef.current = tabKey;
  const currentRef = useRef(currentKey);
  currentRef.current = currentKey;
  const settleTab = () => {
    const keys = Array.from(navRef.current?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? []).map((r) => r.dataset.treeKey ?? "");
    if (keys.length === 0) return;
    const current = currentRef.current;
    const next = keys.includes(tabRef.current) ? tabRef.current : current !== null && keys.includes(current) ? current : keys[0];
    if (next !== tabRef.current) setTabKey(next);
  };
  useLayoutEffect(settleTab);
  useEffect(() => {
    const nav = navRef.current;
    if (!nav || typeof MutationObserver === "undefined") return;
    const observer = new MutationObserver(settleTab);
    observer.observe(nav, { childList: true, subtree: true });
    return () => observer.disconnect();
    // The observer reads the refs, which are current at each change; it is made once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const ctx: Ctx = { ...props, currentKey, tabKey, setTabKey };
  // A file dragged from the desktop onto the tree is not taken and does not open in the browser: an upload is made from the
  // listing, where the drop is read (FilesPage), so the tree only stops the browser's own navigation.
  const fileDrag = (e: DragEvent<HTMLElement>) => {
    if (!Array.from(e.dataTransfer?.types ?? []).includes("Files")) return;
    e.preventDefault();
    if (e.type === "dragover" && e.dataTransfer) e.dataTransfer.dropEffect = "none";
  };
  return (
    <nav ref={navRef} aria-label={t("files.tree.label")} data-testid="folder-tree" onDragOver={fileDrag} onDrop={fileDrag}>
      <ul role="tree" aria-label={t("files.tree.label")} style={{ margin: 0, padding: 0 }}>
        {spaces.map((s) =>
          top && s.id === folder ? null : (
            <Branch
              key={s.id}
              ctx={ctx}
              space={s.id}
              id={null}
              name={s.kind === "personal" ? t("nav.files") : s.name}
              role={s.role}
              level={1}
              above={[]}
              root={s}
            />
          ),
        )}
        {top && <Branch key={top.id} ctx={ctx} space={folder} id={top.id} name={top.name} role={top.role} level={1} above={[]} />}
      </ul>
    </nav>
  );
}
