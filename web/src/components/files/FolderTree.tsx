// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type DragEvent, type KeyboardEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../../lib/apiError";
import { Button, Skeleton, theme as antdTheme } from "antd";
import { ChevronDown, ChevronRight, Folder } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { queryKey } from "../../lib/query";
import { childrenPage } from "../../lib/files/api";
import { fileErrorText } from "../../lib/files/fileErrors";
import { loadDirs } from "../../lib/files/treeLoad";
import { touchExpanded, withExpanded, type TreeState } from "../../lib/files/treeState";
import { clearFailure, closedFor, currentSerial, failureSeenIn, holdClose, holdOpen, noteFailure, startPlace } from "../../lib/files/treeHold";
import { currentMark } from "../../theme/currentMark";
import { groupColours } from "../../theme/groupTint";
import { sidebarRow } from "../../theme/sidebarRow";
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
  // onOpen shows a folder in the listing (a single click).
  onOpen: (folder: string, dir: string | null) => void;
  coarse: boolean;
  // top is the folder the person reaches the place by when the space's own root is not theirs (they were given a folder below
  // it): the tree starts there, in place of the space's root, and only shows what the person can reach.
  top?: { id: string; name: string; role: string | undefined };
  // colours are the sidebar's, when the tree sits in the sidebar: the text, and the mark colour of the current folder (a bar, no background).
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
  // serial counts the changes of place (treeHold): a failed read is shown with its alert only in the visit it was first shown in.
  serial: number;
  // setOpen opens or closes a folder by the person's hand (the arrow and the keys). A close of a folder on the way holds for the place.
  setOpen: (key: string, on: boolean) => void;
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
  // A failed read is announced once (decision 88): its alert is shown in the visit to the place it was first shown in, and a later change of
  // place shows the same failure quietly, with its Try again. A read that succeeds, or Try again, makes the next failure new.
  useEffect(() => {
    if (q.isError) noteFailure(key, ctx.serial);
    else if (q.isSuccess) clearFailure(key);
  }, [q.status, key, ctx.serial]);
  const seenIn = failureSeenIn(key);
  const announce = q.isError && !unreachable && (seenIn === undefined || seenIn === ctx.serial);
  const dirs = q.data?.dirs ?? [];
  // Until it has been read a folder may have folders in it; once read with none, it has no arrow.
  const hasKids = !unreachable && (q.data === undefined || dirs.length > 0 || q.data.capped);
  const current = ctx.currentKey === key;
  const side = ctx.colours;
  // A sidebar row has the menu item's metrics (decision 81: its height, inline margin and radius come from the menu's tokens). Outside
  // the sidebar the rows keep their own: a finger 44 px, a mouse 32 px, no margin.
  const row = side ? sidebarRow(token, ctx.coarse) : null;
  // On a finger the row, its arrow and its link are each 44 px; with a mouse the arrow is 24 and the link 28 in a row of 32.
  const size = ctx.coarse ? 44 : 32;
  const arrow = row ? row.indent : ctx.coarse ? 44 : 24;
  const link = ctx.coarse ? 44 : 28;
  const toggle = () => ctx.setOpen(key, !open);
  // The current place (decision 69, option B) has no background: a bar at the row's start edge, and its text and icon in the
  // mark colour, the same as the menu's selected item (theme/currentMark.ts). The others keep the sidebar's text.
  const mark = side ? currentMark(side) : null;
  // A top-level place (a space or a top folder, level 1) is filled with the sidebar's active colour and its on-active text, with no
  // bar (decision 72). A deeper place keeps the bar.
  const filled = side !== undefined && level === 1 && current;
  const textColour = side ? (filled ? side.onActive : current && mark ? mark.ink : side.text) : undefined;
  // The tint of the group under a space's root (decision 93): only the sidebar's, and only the root's group (id null).
  const colours = side && id === null ? groupColours(side) : undefined;
  const tint = colours?.tint;
  const hover = colours?.hover ?? undefined;
  return (
    <li
      role="treeitem"
      aria-level={level}
      aria-expanded={hasKids ? open : undefined}
      aria-selected={current}
      aria-current={current ? "location" : undefined}
      data-tree-key={key}
      // A folder whose read failed is not the mark while another row on screen is (decision 88): the mark falls to the deepest row that read.
      // A folder with its children still on screen (a refetch that failed) is still a row that read.
      data-failed={q.isError && q.data === undefined ? "" : undefined}
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
        className={side ? (filled ? "tree-row tree-row-side side-current-full" : current ? "tree-row tree-row-side side-current" : "tree-row tree-row-side") : "tree-row"}
        // A double click (a pointer's gesture) opens a folder with subfolders in the tree, so they show (decision 93), as its arrow does. A
        // double click on the arrow is the arrow's own (it toggles on each click), and an open folder stays open.
        onDoubleClick={(e) => {
          if ((e.target as HTMLElement).closest("button")) return;
          if (!hasKids || open) return;
          ctx.setOpen(key, true);
        }}
        data-drop-folder={space}
        data-drop-dir={id ?? ""}
        data-drop-role={role ?? ""}
        data-drop-name={name}
        data-drop-path={path.join(",")}
        style={{
          display: "flex",
          alignItems: "center",
          // A sidebar row's margin is the menu's 4 px, so its arrow takes the icon column's start: no gap and no start padding,
          // and the icon sits where the menu's does (decision 81).
          gap: row ? 0 : 2,
          minBlockSize: row?.height ?? size,
          marginInline: row?.inline,
          paddingInlineStart: (level - 1) * 14 + (row ? 0 : 2),
          borderRadius: row ? row.radius : token.borderRadiusSM,
          color: textColour,
          background: filled ? side?.active : current && !side ? token.colorPrimaryBg : undefined,
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
            // The second click of a double click shows nothing more: the first click showed the folder, and the double click opens it
            // (decision 93), so the folder is shown once.
            if (e.detail > 1) return;
            ctx.onOpen(space, id);
          }}
          style={{ display: "flex", alignItems: "center", gap: 10, flex: "1 1 auto", minInlineSize: 0, minBlockSize: link, color: "inherit", textDecoration: "none" }}
        >
          {/* The icon is the sidebar's (18 px, as the menu's), and the name follows it 10 px on: the same columns as the menu rows (decision 66). */}
          <Folder size={18} strokeWidth={1.75} aria-hidden="true" style={{ flex: "none" }} />
          <bdi style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minInlineSize: 0 }}>{name}</bdi>
          {root && ctx.spaceLabel?.(root)}
        </a>
        {root && ctx.spaceEnd?.(root)}
      </div>
      {open && (
        // The group under a space's root is drawn with the sidebar's tint, one block per root (decision 93): the tint is a CSS
        // variable, painted behind the rows by index.css, so the rows' own inset (the menu's) is kept. Nested groups have none.
        <ul
          role="group"
          data-tree-group={tint ? "root" : undefined}
          className={tint ? "tree-group-root" : undefined}
          style={{
            margin: 0,
            padding: 0,
            ...(tint ? ({ "--sc-group-tint": tint, "--sc-group-inset": `${row?.inline ?? 4}px`, "--sc-group-hover": hover } as CSSProperties) : undefined),
          }}
        >
          {q.isPending && (
            <li role="none" aria-busy="true" style={{ listStyle: "none", paddingInlineStart: level * 14 + 8 }}>
              <span className="sr-only">{t("files.loading")}</span>
              <Skeleton active title={false} paragraph={{ rows: 1, width: 80 }} />
            </li>
          )}
          {q.isError && !unreachable && (
            <li role="none" style={{ listStyle: "none", paddingInlineStart: level * 14 + 8 }}>
              <div role={announce ? "alert" : undefined} style={{ color: side ? side.text : token.colorError, fontSize: token.fontSizeSM }}>
                {t("files.tree.failed")} <bdi>{fileErrorText(q.error, lang)}</bdi>
              </div>
              <Button
                size="small"
                onClick={() => {
                  clearFailure(key);
                  void q.refetch();
                }}
                style={ctx.coarse ? { minBlockSize: 44 } : undefined}
              >
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
  const { spaces, folder, chain, marking, top } = props;
  // The place's mark (decisions 69 and 88): the folder shown, or else the nearest folder on the way to it that is on screen and has not
  // failed to read. The places on the way, deepest first, are read in settleTab; the first one that is eligible is the mark.
  const wanted = marking && folder ? [...[...chain].reverse(), folder] : [];
  // The way (decision 88): the folders that open so the folder shown is on screen, from the space's root down, or from the top folder
  // the person was given. The folder shown is not on it: it is marked, not opened. A space's own root has no way.
  const way = marking && folder && chain.length > 0 ? (top ? chain.slice(0, -1) : [folder, ...chain.slice(0, -1)]) : [];
  const placeText = marking && folder ? [folder, ...chain, top?.id ?? ""].join("|") : "";
  // The refs the reveal and the observer read. They are written in effects, never during render (a render React discards must not move them).
  const wantedRef = useRef<string[]>([]);
  const wayRef = useRef<string[]>([]);
  const placeRef = useRef("");
  const propsRef = useRef(props);
  // latestRef is the state this tree last sent, so two changes in one tick build on each other, not on the last render's props.
  const latestRef = useRef<TreeState>(props.state);
  // seenStateRef is the parent's state the last render was given: latestRef moves only when a new one comes in.
  const seenStateRef = useRef<TreeState>(props.state);
  const alive = useRef(false);
  const revealQueued = useRef(false);
  const navRef = useRef<HTMLElement | null>(null);
  const currentRef = useRef<string | null>(null);
  const [currentKey, setCurrentKey] = useState<string | null>(null);
  // bump renders the tree again when the place changes, so the rows read the new visit at once.
  const [, bump] = useState(0);
  // The tree is one tab stop, and it is always on a row that is rendered: the place's row when it is shown, else the first row.
  // It is checked after each change of the tree, including a branch that opens, closes or loses its rows (the observer).
  const [tabKey, setTabKey] = useState<string>(spaces[0]?.id ?? "");
  const tabRef = useRef(tabKey);
  useEffect(() => {
    if (currentKey !== null) setTabKey(currentKey);
  }, [currentKey]);
  // The layout effects below run in this order, after each render: the alive flag and the refs first, then the place's hold, then the settle.
  useLayoutEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useLayoutEffect(() => {
    wantedRef.current = wanted;
    wayRef.current = way;
    tabRef.current = tabKey;
    propsRef.current = props;
    // latestRef takes a state only when the parent sends a new one. A render that carries the state the tree sent earlier (before the parent
    // has applied it) must not take the tree back to that state: the next change builds on what the tree last sent (decision 88, review L5).
    if (props.state !== seenStateRef.current) {
      seenStateRef.current = props.state;
      latestRef.current = props.state;
    }
  });
  // A change of place starts its hold (decision 88): a folder the person closed by hand on the way stays closed for that place, and the next
  // change of place reveals again. The change renders again at once, so the failed reads already shown are quiet in that render.
  useLayoutEffect(() => {
    if (placeRef.current === placeText) return;
    placeRef.current = placeText;
    if (startPlace(placeText)) bump((n) => n + 1);
  }, [placeText]);
  // commit sends a new state to the parent and keeps it here for the next change in the same tick.
  const commit = (next: TreeState) => {
    latestRef.current = next;
    propsRef.current.onChange(next);
  };
  // setOpen is the person's own open or close. A close of a folder on the way holds for the place; an open of one releases it.
  const setOpen = (key: string, on: boolean) => {
    if (wayRef.current.includes(key)) {
      if (on) holdOpen(placeRef.current, key);
      else holdClose(placeRef.current, key);
    }
    commit(withExpanded(latestRef.current, key, on, wayRef.current));
  };
  // reveal opens, by one step, the first folder on the way that is shown and not open (decision 88). A folder below it is shown only once the
  // one above has been read, so the way is read top down, once each. A folder the person closed by hand for this place stops the reveal, and a
  // folder that is not shown stops it too. Each step is one update made in a microtask, not a nested update inside a commit, so a way of any
  // length settles (the update depth limit of React counts nested commits). The way is pinned while it is revealed, so the saved cap does not
  // forget it, and the folders the place passes through are touched so the cap forgets by last use.
  const reveal = () => {
    revealQueued.current = false;
    if (!alive.current || placeRef.current === "") return;
    const way = wayRef.current;
    const rows = new Set(Array.from(navRef.current?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? []).map((r) => r.dataset.treeKey ?? ""));
    const closed = closedFor(placeRef.current);
    const state = touchExpanded(latestRef.current, way);
    for (const k of way) {
      if (!rows.has(k)) break;
      if (!state.expanded.includes(k)) {
        if (closed.includes(k)) break;
        commit(withExpanded(state, k, true, way));
        return;
      }
    }
    if (state !== latestRef.current) commit(state);
  };
  const schedule = () => {
    if (revealQueued.current) return;
    revealQueued.current = true;
    queueMicrotask(reveal);
  };
  const settleTab = () => {
    const rows = new Map(
      Array.from(navRef.current?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? []).map((r) => [r.dataset.treeKey ?? "", r] as const),
    );
    // The viewed folder is the mark when it is shown. A folder on the way is the mark when it is shown and did not fail to read; while it
    // is being read it is the nearest row on screen, so the mark does not flicker.
    const mark = wantedRef.current.find((k, i) => {
      const row = rows.get(k);
      return row !== undefined && (i === 0 || row.dataset.failed === undefined);
    }) ?? null;
    if (mark !== currentRef.current) {
      currentRef.current = mark;
      setCurrentKey(mark);
    }
    if (wayRef.current.length > 0) schedule();
    const keys = Array.from(rows.keys());
    if (keys.length === 0) return;
    const next = keys.includes(tabRef.current) ? tabRef.current : (mark ?? keys[0]);
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
  // The mark is shown only while the tree marks (the files pages): leaving them takes it off at once, whatever the last read of it said.
  const ctx: Ctx = { ...props, currentKey: marking ? currentKey : null, tabKey, setTabKey, setOpen, serial: currentSerial() };
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
