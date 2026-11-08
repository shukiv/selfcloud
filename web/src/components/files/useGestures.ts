// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { bandSelection, boxBetween, edgeScroll, movedFar, treeNode, type Box, type Decision, type Dest, type Why } from "../../lib/files/drag";
import type { Node } from "../../lib/files/types";

// The pointer gestures of the file browser, with a mouse (a finger scrolls and a pen has no touch-action to keep the browser from panning, so both use Move to and the menu): a press on an item that moves becomes a drag (move, or copy
// with Ctrl) onto a folder; a press on empty space that moves becomes a rubber band. Both are made of pointer events, not of the
// browser's own drag and drop. That keeps them apart from the files dragged in from the desktop, which are the browser's drag
// and drop and upload: an item's drag never fires dragenter or dragover, and a desktop drag never fires a pointer event.
//
// The page says what is allowed (GestureCtl); this file only watches the pointer, finds what it is over, and tells the page.
// A place that can take a drop carries data-drop-folder, data-drop-dir ("" for a folder's root), data-drop-role, data-drop-name
// and, in the tree, data-drop-path. The state of the place under the pointer is data-drop-state, "ok" or "refused", set here and
// styled in index.css, so no re-render follows each move of the pointer.

export interface GestureCtl {
  // enabled says gestures may begin: a place is shown, and no dialog or rename is open.
  enabled: () => boolean;
  // startDrag says the item with this id was pressed and has moved. It returns the items the drag carries: the whole
  // selection when the item is in it, else that item alone (which the page selects). null stops the drag.
  startDrag: (id: string) => Node[] | null;
  // startTree says a folder of the sidebar's tree was pressed and has moved (decision 104). The folder is the one the drag carries:
  // the listing's selection is not changed by it. null stops the drag.
  startTree: (folder: Node) => Node[] | null;
  decide: (nodes: Node[], dest: Dest, copy: boolean) => Decision;
  drop: (decision: Extract<Decision, { ok: true }>, dest: Dest) => void;
  refuse: (why: Why) => void;
  selectedIDs: () => string[];
  setBand: (ids: string[]) => void;
  clear: () => void;
}

// DragView is what the drag shows beside the pointer: what is carried, what the mode is, and the place under the pointer.
export interface DragView {
  count: number;
  name: string;
  copy: boolean;
  target: string | null;
  why: Why | null;
}

// Where a press does not begin a gesture: controls, the toolbar and the column header, text being edited, open menus.
const SKIP =
  'button, a, input, textarea, select, label, [contenteditable="true"], .ant-checkbox-wrapper, .ant-table-selection-column, [role="toolbar"], nav, thead, [role="menu"], [role="alert"], .ant-alert, .ant-dropdown, .ant-modal-root, .ant-drawer';

const FRAME_GAP = 14;

type Press =
  | { kind: "row"; id: string; x: number; y: number }
  | { kind: "tree"; folder: Node; x: number; y: number }
  | { kind: "band"; x: number; y: number; ax: number; ay: number; base: string[] };

// destOf reads a place from the attributes its element carries.
export function destOf(el: HTMLElement): Dest {
  const d = el.dataset;
  if (el.hasAttribute("data-drop-trash")) return { folder: "", dir: null, role: undefined, trash: true };
  return { folder: d.dropFolder ?? "", dir: d.dropDir ? d.dropDir : null, role: d.dropRole || undefined, path: d.dropPath ? d.dropPath.split(",") : undefined };
}

// treeOf reads the folder a sidebar row says it is, from the attributes FolderTree gives a drag source (data-tree-*), or null.
function treeOf(el: HTMLElement): Node | null {
  const d = el.dataset;
  if (!d.treeDrag) return null;
  return treeNode({ id: d.treeDrag, space: d.treeSpace ?? "", parent: d.treeParent ? d.treeParent : null, name: d.treeName ?? "", role: d.treeRole || undefined });
}

// The controls of a sidebar row: an arrow is the row's own, and a press on it is not a drag.
const TREE_SKIP = 'button, input, textarea, select, label, [contenteditable="true"], [role="menu"], .ant-dropdown, .ant-modal-root, .ant-drawer';

// sideBox is the sidebar's scroll region, when the pointer is inside it: a drag there scrolls it at its edges (decision 104).
function sideBox(x: number, y: number): { el: HTMLElement; top: number; bottom: number } | null {
  const el = document.querySelector<HTMLElement>(".shell-scroll");
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (x < r.left || x > r.right || y < r.top || y > r.bottom) return null;
  return { el, top: r.top, bottom: r.bottom };
}

// scrollerOf is the nearest ancestor that scrolls, or null when the page itself does.
function scrollerOf(el: HTMLElement | null): HTMLElement | null {
  for (let p = el?.parentElement ?? null; p && p !== document.body && p !== document.documentElement; p = p.parentElement) {
    const o = getComputedStyle(p).overflowY;
    if ((o === "auto" || o === "scroll") && p.scrollHeight > p.clientHeight) return p;
  }
  return null;
}

export function useGestures(rootRef: RefObject<HTMLElement | null>, listRef: RefObject<HTMLElement | null>, ctl: RefObject<GestureCtl | null>) {
  const [drag, setDrag] = useState<DragView | null>(null);
  const [band, setBand] = useState(false);
  const ghostRef = useRef<HTMLDivElement | null>(null);
  const bandRef = useRef<HTMLDivElement | null>(null);
  // place is the last pointer position, and sets where the ghost is when it first mounts.
  const place = useRef<{ x: number; y: number } | null>(null);
  const dragRef = useRef<DragView | null>(null);

  const putGhost = () => {
    const el = ghostRef.current;
    const p = place.current;
    if (!el || !p) return;
    const rtl = document.documentElement.dir === "rtl";
    const width = document.documentElement.clientWidth;
    el.style.insetInlineStart = `${rtl ? width - p.x + FRAME_GAP : p.x + FRAME_GAP}px`;
    el.style.insetBlockStart = `${p.y + FRAME_GAP}px`;
  };
  useLayoutEffect(putGhost, [drag === null]);

  useEffect(() => {
    let press: Press | null = null;
    let mode: "none" | "drag" | "band" = "none";
    let carried: Node[] = [];
    let last = { x: 0, y: 0, ctrl: false };
    let over: HTMLElement | null = null;
    let overKey = "";
    let bandKey = "";
    let frame = 0;

    const clearOver = () => {
      if (over) {
        delete over.dataset.dropState;
        delete over.dataset.dropWhy;
      }
      over = null;
      overKey = "";
    };

    // visible is the part of the window in which rows can be seen: below the toolbar and the pinned column header, above the bottom
    // of the scrolling area. A band's pointer end stops there, and only that part of it is drawn.
    const visible = () => {
      const root = rootRef.current;
      const scroller = scrollerOf(root);
      const sr = scroller?.getBoundingClientRect();
      const bar = root?.querySelector<HTMLElement>('[role="toolbar"]')?.getBoundingClientRect().bottom ?? 0;
      const head = root?.querySelector<HTMLElement>("thead")?.getBoundingClientRect().bottom ?? 0;
      return { top: Math.max(0, sr?.top ?? 0, bar, head), bottom: Math.min(window.innerHeight, sr?.bottom ?? window.innerHeight) };
    };

    // update finds what the pointer is over and shows it.
    const update = () => {
      const c = ctl.current;
      if (!c) return;
      place.current = { x: last.x, y: last.y };
      if (mode === "drag") {
        putGhost();
        const el = document.elementFromPoint(last.x, last.y)?.closest<HTMLElement>("[data-drop-folder], [data-drop-trash]") ?? null;
        const key = `${el ? "t" : "-"}|${last.ctrl}`;
        if (el === over && key === overKey) return;
        if (el !== over) clearOver();
        over = el;
        overKey = key;
        let view: DragView;
        const base = { count: carried.length, name: carried.length === 1 ? carried[0].name : "", copy: last.ctrl };
        if (el) {
          const dec = c.decide(carried, destOf(el), last.ctrl);
          el.dataset.dropState = dec.ok ? "ok" : "refused";
          if (!dec.ok) el.dataset.dropWhy = dec.why;
          view = { ...base, target: el.dataset.dropName ?? "", why: dec.ok ? null : dec.why };
        } else {
          view = { ...base, target: null, why: null };
        }
        dragRef.current = view;
        setDrag(view);
        return;
      }
      if (mode === "band" && press?.kind === "band") {
        const root = rootRef.current;
        if (!root) return;
        const rr = root.getBoundingClientRect();
        const vis = visible();
        // The pointer end of the band stops at the edge of what can be seen, so the band never reaches over the toolbar or the pinned
        // header, nor takes the rows hidden under them. The other end stays where the press was, in the list, so rows that have
        // scrolled out of the window on the way are still between the two.
        const py = Math.min(Math.max(last.y, vis.top), vis.bottom) - rr.top;
        const box = boxBetween(press.ax, press.ay, last.x - rr.left, py);
        const items = Array.from(listRef.current?.querySelectorAll<HTMLElement>("[data-id]") ?? []).map((el) => {
          const r = el.getBoundingClientRect();
          return { id: el.dataset.id ?? "", box: { x0: r.left - rr.left, y0: r.top - rr.top, x1: r.right - rr.left, y1: r.bottom - rr.top } as Box };
        });
        const ids = bandSelection(items, box, press.base, last.ctrl);
        const k = ids.join(",");
        if (k !== bandKey) {
          bandKey = k;
          c.setBand(ids);
        }
        const el = bandRef.current;
        if (el) {
          const cw = document.documentElement.clientWidth;
          const start = Math.max(0, rr.left + box.x0);
          const end = Math.min(cw, rr.left + box.x1);
          // Only the part of the band in the window is drawn, below the toolbar and the header.
          const top = Math.max(vis.top, rr.top + box.y0);
          const bottom = Math.min(vis.bottom, rr.top + box.y1);
          const rtl = document.documentElement.dir === "rtl";
          el.style.insetInlineStart = `${rtl ? cw - end : start}px`;
          el.style.insetBlockStart = `${top}px`;
          el.style.inlineSize = `${Math.max(0, end - start)}px`;
          el.style.blockSize = `${Math.max(0, bottom - top)}px`;
        }
      }
    };

    // The page scrolls under a pointer held near the top or the bottom of the list, so a band or a drag can reach what is off screen.
    const tick = () => {
      frame = 0;
      if (mode === "none") return;
      // A drag held in the sidebar's edge band scrolls the sidebar, so a folder far down the tree can be reached (decision 104).
      // The listing is not scrolled then: the pointer is not over it.
      const side = mode === "drag" ? sideBox(last.x, last.y) : null;
      if (side) {
        const dy = edgeScroll(last.y, side.top, side.bottom);
        if (dy !== 0) {
          const before = side.el.scrollTop;
          side.el.scrollTop += dy;
          if (side.el.scrollTop !== before) {
            overKey = "";
            update();
          }
        }
        frame = requestAnimationFrame(tick);
        return;
      }
      const root = rootRef.current;
      const scroller = scrollerOf(root);
      const bar = root?.querySelector<HTMLElement>('[role="toolbar"]');
      const rect = scroller?.getBoundingClientRect();
      const top = Math.max(rect?.top ?? 0, bar?.getBoundingClientRect().bottom ?? 0);
      const bottom = rect?.bottom ?? window.innerHeight;
      const dy = edgeScroll(last.y, top, bottom);
      if (dy !== 0) {
        const before = scroller ? scroller.scrollTop : window.scrollY;
        if (scroller) scroller.scrollTop += dy;
        else window.scrollBy(0, dy);
        const after = scroller ? scroller.scrollTop : window.scrollY;
        if (after !== before) {
          overKey = "";
          bandKey = "";
          update();
        }
      }
      frame = requestAnimationFrame(tick);
    };

    const swallowClick = () => {
      const stop = (ev: MouseEvent) => {
        ev.stopPropagation();
        ev.preventDefault();
      };
      window.addEventListener("click", stop, true);
      setTimeout(() => window.removeEventListener("click", stop, true), 50);
    };

    const noSelect = (ev: Event) => ev.preventDefault();

    const finish = (commit: boolean) => {
      window.removeEventListener("pointermove", onMove, true);
      window.removeEventListener("pointerup", onUp, true);
      window.removeEventListener("pointercancel", onCancel, true);
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("keyup", onKey, true);
      window.removeEventListener("blur", onCancel);
      window.removeEventListener("selectstart", noSelect, true);
      window.removeEventListener("dragstart", noSelect, true);
      if (frame) cancelAnimationFrame(frame);
      frame = 0;
      document.documentElement.classList.remove("sc-gesture");
      const c = ctl.current;
      const wasMode = mode;
      const wasPress = press;
      const target = over;
      const nodes = carried;
      const ctrl = last.ctrl;
      clearOver();
      mode = "none";
      press = null;
      carried = [];
      bandKey = "";
      dragRef.current = null;
      setDrag(null);
      setBand(false);
      if (wasMode !== "none") swallowClick();
      if (!c || !commit) return;
      if (wasMode === "drag" && target) {
        const dest = destOf(target);
        const dec = c.decide(nodes, dest, ctrl);
        if (dec.ok) c.drop(dec, dest);
        else c.refuse(dec.why);
      } else if (wasMode === "none" && wasPress?.kind === "band" && !ctrl) {
        // A click on empty space, with no band drawn, clears the selection.
        c.clear();
      }
    };

    const onMove = (e: PointerEvent) => {
      if (!press) return;
      last = { x: e.clientX, y: e.clientY, ctrl: e.ctrlKey || e.metaKey };
      // The button was let go where the page could not hear it.
      if (e.buttons === 0) {
        finish(false);
        return;
      }
      if (mode === "none") {
        if (!movedFar(e.clientX - press.x, e.clientY - press.y)) return;
        const c = ctl.current;
        if (!c) return;
        if (press.kind === "row" || press.kind === "tree") {
          const nodes = press.kind === "row" ? c.startDrag(press.id) : c.startTree(press.folder);
          if (!nodes || nodes.length === 0) {
            finish(false);
            return;
          }
          carried = nodes;
          mode = "drag";
        } else {
          mode = "band";
          setBand(true);
        }
        document.documentElement.classList.add("sc-gesture");
        window.getSelection()?.removeAllRanges();
        frame = requestAnimationFrame(tick);
      }
      update();
    };

    const onUp = (e: PointerEvent) => {
      last = { x: e.clientX, y: e.clientY, ctrl: e.ctrlKey || e.metaKey };
      if (mode === "drag") update();
      finish(true);
    };

    const onCancel = () => finish(false);

    const onKey = (e: KeyboardEvent) => {
      // Escape ends a drag or a band, and only then is it taken: a press that has not moved yet leaves it to the page.
      if (e.type === "keydown" && e.key === "Escape" && mode !== "none") {
        e.preventDefault();
        e.stopPropagation();
        finish(false);
        return;
      }
      if (e.key === "Control" || e.key === "Meta") {
        last = { ...last, ctrl: e.type === "keydown" };
        if (mode !== "none") update();
      }
    };

    const onDown = (e: PointerEvent) => {
      if (press || e.button !== 0 || e.pointerType !== "mouse" || !e.isPrimary) return;
      const c = ctl.current;
      const root = rootRef.current;
      const t = e.target;
      if (!c || !(t instanceof Element) || !c.enabled()) return;
      if (t === document.documentElement) return;
      // A press that only dismisses an open menu does nothing else.
      if (document.querySelector(".ant-dropdown:not(.ant-dropdown-hidden)")) return;
      // A folder of the sidebar's tree is a drag source (decision 104): a press on its name or its row moves it; its arrow and its menu are
      // the controls' own. Its name is a link, so a press that does not move is the click that opens the folder, as before.
      const tree = t.closest<HTMLElement>("[data-tree-drag]");
      if (tree) {
        if (t.closest(TREE_SKIP)) return;
        const folder = treeOf(tree);
        if (!folder) return;
        press = { kind: "tree", folder, x: e.clientX, y: e.clientY };
      } else {
        if (!root || !root.contains(t) || t.closest(SKIP)) return;
        const row = t.closest<HTMLElement>("[data-id]");
        if (row && listRef.current?.contains(row)) {
          press = { kind: "row", id: row.dataset.id ?? "", x: e.clientX, y: e.clientY };
        } else {
          const rr = root.getBoundingClientRect();
          press = { kind: "band", x: e.clientX, y: e.clientY, ax: e.clientX - rr.left, ay: e.clientY - rr.top, base: c.selectedIDs() };
        }
      }
      last = { x: e.clientX, y: e.clientY, ctrl: e.ctrlKey || e.metaKey };
      window.addEventListener("pointermove", onMove, true);
      window.addEventListener("pointerup", onUp, true);
      window.addEventListener("pointercancel", onCancel, true);
      window.addEventListener("keydown", onKey, true);
      window.addEventListener("keyup", onKey, true);
      window.addEventListener("blur", onCancel);
      window.addEventListener("selectstart", noSelect, true);
      window.addEventListener("dragstart", noSelect, true);
    };

    window.addEventListener("pointerdown", onDown, true);
    return () => {
      window.removeEventListener("pointerdown", onDown, true);
      if (press) finish(false);
    };
    // The refs are stable; the page's answers are read through ctl at the moment they are needed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { drag, band, ghostRef, bandRef };
}
