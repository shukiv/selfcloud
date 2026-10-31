// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { useLocation } from "react-router-dom";
import { useInfiniteQuery, useQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { Alert, Button, Form, Input, Modal, Select, Skeleton, theme as antdTheme } from "antd";
import { useLang, useT, translatePlural } from "../lib/i18n";
import { useStore, get } from "../lib/store";
import { brand } from "../lib/brand";
import { ApiError } from "../lib/apiError";
import { go } from "../lib/router";
import { session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { queryKey, refreshTree } from "../lib/query";
import { folders, hiddenFolders, loadFolders, loadHiddenFolders } from "../lib/files/folders";
import { keepTargetOnSave } from "../lib/files/appearance";
import { childrenPage, copyNode, deleteNode, getJob, getMe, mkdir, moveNode, renameNode, restoreNode, serverLimits, type Transfer } from "../lib/files/api";
import { locate, joinPath, type Here } from "../lib/files/location";
import { starNode, unstarNode } from "../lib/stars";
import { fileErrorText } from "../lib/files/fileErrors";
import { nameProblem, nameMessageKey, uploadTargetFor, fromFileList, fromDataTransfer, startDownload, type DroppedFile } from "../lib/files/ops";
import { parseFilesPath, filesURL, type FilesRoute } from "../lib/files/route";
import { trimCrumbs, type Crumb } from "../lib/files/breadcrumbs";
import { sortNodes, nextSort, type SortDir, type SortKey } from "../lib/files/sort";
import { clickSelect, moveFocus, selectAll, EMPTY, type Selection } from "../lib/files/selection";
import { keyAction } from "../lib/files/keys";
import { can, downloadAllowed, atLeast } from "../lib/files/roles";
import { shareCap } from "../lib/sharing/roles";
import { previewable } from "../lib/files/kind";
import { readPrefs, writePrefs, type ViewPrefs } from "../lib/files/viewPrefs";
import { UndoStack } from "../lib/files/undo";
import type { ConflictChoice } from "../lib/files/conflict";
import type { Job, Node, Page } from "../lib/files/types";
import { manager, uploads } from "../lib/files/uploads";
import Breadcrumbs, { type Place } from "../components/files/Breadcrumbs";
import Listing, { type Click } from "../components/files/Listing";
import Details from "../components/files/Details";
import Preview from "../components/files/Preview";
import Versions from "../components/files/Versions";
import PickDestination, { type Destination } from "../components/files/PickDestination";
import AddShortcut from "../components/files/AddShortcut";
import RequestAccess from "../components/share/RequestAccess";
import { openShortcut } from "../lib/files/shortcut";
import Conflict from "../components/files/Conflict";
import FocusOnOpen from "../components/FocusOnOpen";
import UndoToast from "../components/UndoToast";
import NotFoundPage from "./NotFoundPage";

// The file browser: a folder or a directory, its items, and what the signed-in person may do with them.
// The server checks every operation again; the buttons only show what the role allows.
//
// The share dialog is loaded on first use: it is not part of the file browser's first load.
const ShareDialog = lazy(() => import("../components/share/ShareDialog"));

// Sharing from this page: the Share button in the selection bar (when the caller may share the item), Share and
// "Who has access" in Details, and the access request on a not-found directory.

type Dialog = "newFolder" | "delete" | "pick" | "details" | "versions" | "preview" | "share" | "pickShortcut" | "shortcut" | "unavailable" | null;

// Place is where the browser is, or why it cannot show it.
type PlaceState =
  | { kind: "ready"; here: Here; personalID: string | null }
  | { kind: "missing" }
  | { kind: "redirect"; to: string };

// SelState keeps IDs next to positions, so a sort or a page that arrives later cannot move the selection.
interface SelState extends Selection {
  anchorID: string | null;
  focusID: string | null;
}

const NO_SEL: SelState = { ...EMPTY, anchorID: null, focusID: null };

const toolbarStyle = { display: "flex", flexWrap: "wrap" as const, alignItems: "center", gap: 8, width: "100%" };

interface RenameState {
  id: string;
  value: string;
  error: string | null;
}

// loadPlace reads where the browser is. A missing folder or directory, and an address that is not a folder
// address, are missing; the caller's own folder is read for /files.
async function loadPlace(route: FilesRoute): Promise<PlaceState> {
  if (!route || route.kind === "node") return { kind: "missing" };
  if (route.kind === "my") {
    const me = await getMe();
    if (!me.personal_folder_id) return { kind: "missing" };
    return { kind: "redirect", to: filesURL(me.personal_folder_id, null) };
  }
  const personalID = (await getMe().catch(() => null))?.personal_folder_id ?? null;
  let list = get(folders).length > 0 ? get(folders) : await loadFolders().catch(() => []);
  // A hidden space is not in the visible list, but it is still reached, so its name is read here.
  if (!list.some((f) => f.id === route.folder)) {
    list = [...list, ...(await loadHiddenFolders().catch(() => []))];
  }
  const dirID = route.kind === "dir" ? route.dir : null;
  try {
    const where = await locate(route.folder, dirID, list);
    if (!where) return { kind: "missing" };
    return { kind: "ready", here: where, personalID };
  } catch (err) {
    if (err instanceof ApiError && err.code === "not_found") return { kind: "missing" };
    throw err;
  }
}

export default function FilesPage() {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  const qc = useQueryClient();
  const location = useLocation();
  const path = location.pathname;
  const search = location.search;
  const route = useMemo(() => parseFilesPath(path), [path]);
  const brandName = useStore(brand).name;
  const folderList = useStore(folders);
  const hiddenList = useStore(hiddenFolders);
  const uploadList = useStore(uploads);


  // Nothing is read without a session: a sign-out clears the session before the page leaves, and a read that
  // followed it would be refused.
  const signedIn = useStore(session) !== null;
  const [prefs, setPrefs] = useState<ViewPrefs>(() => readPrefs());
  const [sel, setSel] = useState<SelState>(NO_SEL);
  const [notice, setNotice] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const listRef = useRef<HTMLDivElement | null>(null);
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const sectionRef = useRef<HTMLElement | null>(null);
  const folderInputRef = useRef<HTMLInputElement | null>(null);

  const [rename, setRenameState] = useState<RenameState | null>(null);
  const renameRef = useRef<RenameState | null>(null);
  const setRename = (next: RenameState | null) => {
    renameRef.current = next;
    setRenameState(next);
  };

  const [dialog, setDialog] = useState<Dialog>(null);
  const [pickMode, setPickMode] = useState<"move" | "copy">("move");
  const [target, setTarget] = useState<Node | null>(null);
  // dialogRef is the dialog as of the last render, for a callback that may run after the dialog has changed.
  const dialogRef = useRef<Dialog>(null);
  dialogRef.current = dialog;
  const [shortcutDest, setShortcutDest] = useState<Destination | null>(null);
  // A change of person (a sign-out, or a sign-in as someone else) closes whatever dialog was open: its answers
  // were given to the previous person.
  const sessionID = sessionKey(useStore(session));
  useEffect(() => {
    setDialog((d) => (d === "share" ? null : d));
  }, [sessionID]);
  const [pending, setPending] = useState<Node[]>([]);
  const [previewItems, setPreviewItems] = useState<Node[]>([]);
  const [previewIndex, setPreviewIndex] = useState(0);
  const [newName, setNewName] = useState("");
  const [newNameError, setNewNameError] = useState<string | null>(null);
  const [jobs, setJobs] = useState<Array<{ id: string; status: Job["status"] }>>([]);
  const [conflict, setConflict] = useState<{ name: string; batch: boolean } | null>(null);
  const conflictResolve = useRef<((a: { choice: ConflictChoice; remember: boolean }) => void) | null>(null);
  const undoStack = useRef<UndoStack | null>(null);
  if (undoStack.current === null) undoStack.current = new UndoStack();
  const [undoView, setUndoView] = useState<{ label: string; expiresAt: number; status: string } | null>(null);
  // The uploads already finished when the page opened are not new: only a finish seen while mounted reloads.
  const seenDone = useRef<number | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      // A question still open is answered skip, so the transfer waiting on it ends instead of hanging.
      const pending = conflictResolve.current;
      conflictResolve.current = null;
      pending?.({ choice: "skip", remember: false });
    };
  }, []);

  // ---- the place and its listing ----

  const placeQ = useQuery({
    queryKey: queryKey("place", path),
    // A visit reads the place again, whatever the cache holds: a rename or a move elsewhere changes it.
    refetchOnMount: "always",
    queryFn: () => loadPlace(route),
    enabled: signedIn && route !== null && route.kind !== "node",
  });
  const place = placeQ.data;
  const here = place?.kind === "ready" ? place.here : null;
  const personalID = place?.kind === "ready" ? place.personalID : null;
  const folderID = here?.folder ?? null;
  const dirID = here?.dir?.id ?? null;

  const childrenQ = useInfiniteQuery({
    queryKey: queryKey("children", folderID, dirID),
    refetchOnMount: "always",
    queryFn: ({ pageParam }: { pageParam: string | undefined }) => childrenPage(folderID as string, dirID, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<Node>) => last.next_cursor,
    enabled: signedIn && here !== null,
  });
  const items = useMemo<Node[]>(() => (childrenQ.data?.pages ?? []).flatMap((p) => p.items), [childrenQ.data]);
  const hasMore = childrenQ.hasNextPage === true;

  // A place change starts with nothing selected and no notice, as a fresh load did. It also closes whatever dialog
  // or preview was open: a dialog belongs to the address it was opened at, and Back or a link must not leave it on
  // top. This runs before the effect that opens a ?preview= address, so that address still opens its preview.
  useEffect(() => {
    setSel(NO_SEL);
    setNotice(null);
    setDialog(null);
    setTarget(null);
    setPreviewItems([]);
  }, [path]);

  // Redirect /files to the caller's own folder.
  useEffect(() => {
    if (place?.kind === "redirect") go(place.to, { replace: true });
  }, [place]);

  // The title a person had before the browser is given back when the browser is left.
  const titleBefore = useRef(document.title);
  useEffect(() => {
    const before = titleBefore.current;
    return () => {
      document.title = before;
    };
  }, []);
  useEffect(() => {
    if (here) document.title = `${here.dir?.name ?? here.folderName ?? t("nav.files")} · ${brandName}`;
  }, [here, brandName, t]);

  // Uploads that finish add to the listing, so the listing reads again once for each one.
  const doneCount = uploadList.filter((u) => u.status === "done").length;
  useEffect(() => {
    if (seenDone.current !== null && doneCount > seenDone.current) void reload();
    seenDone.current = doneCount;
    // reload reads the current listing from the query client, so it is not a dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doneCount]);

  useEffect(() => {
    void serverLimits()
      .then((l) => manager.setChunkSize(l.max_chunk_size))
      .catch(() => undefined);
  }, []);

  // Items past the first page load when the end comes into view; the button does the same.
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasMore || childrenQ.isFetchingNextPage || typeof IntersectionObserver === "undefined") return;
    const obs = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) void loadMore();
    });
    obs.observe(el);
    return () => obs.disconnect();
    // loadMore reads the query client's state at call time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasMore, childrenQ.isFetchingNextPage, items.length]);

  // applyQuery honours ?select= (highlight an item) and ?preview= (open a file's preview), as a node link does.
  // An item past the first pages is looked for on the pages after it.
  // Each address is applied once for each time it is named, as the Svelte page did on every change of address.
  const appliedFor = useRef<string | null>(null);
  const refreshedFor = useRef<string | null>(null);
  useEffect(() => {
    appliedFor.current = null;
    refreshedFor.current = null;
  }, [path, search]);
  useEffect(() => {
    const q = new URLSearchParams(search);
    const want = q.get("preview") ?? q.get("select");
    if (!want || !here || childrenQ.isPending) return;
    const key = `${path}?${search}`;
    if (appliedFor.current === key) return;
    const found = items.find((n) => n.id === want);
    if (!found) {
      if (hasMore && !childrenQ.isFetchingNextPage && (childrenQ.data?.pages.length ?? 0) < 40) {
        void childrenQ.fetchNextPage();
      } else if (refreshedFor.current !== key) {
        // The listing may be a cached one that predates the item: read the first pages once more before giving up.
        refreshedFor.current = key;
        void childrenQ.refetch();
      } else {
        appliedFor.current = key;
      }
      return;
    }
    appliedFor.current = key;
    setSel({ ids: [found.id], anchor: null, focus: null, anchorID: found.id, focusID: found.id });
    if (q.get("preview") && previewable(found)) openPreview(found);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, search, path, here, childrenQ.isPending]);

  // reload reads the listing again, keeping the place. Selected items that are gone are dropped.
  function reload(): Promise<unknown> {
    return refreshTree(qc);
  }

  async function loadMore(): Promise<void> {
    if (!here || !hasMore || childrenQ.isFetchingNextPage) return;
    try {
      await childrenQ.fetchNextPage();
    } catch (err) {
      setNotice(fileErrorText(err, lang));
    }
  }

  // A failed read after the listing is already shown does not replace it: the failure is shown as the notice.
  const readFailure = childrenQ.isError && childrenQ.data !== undefined ? fileErrorText(childrenQ.error, lang) : null;

  // Selection kept after a listing changes: IDs that are no longer listed are dropped.
  useEffect(() => {
    setSel((prev) => {
      if (prev.ids.length === 0) return prev;
      const present = new Set(items.map((n) => n.id));
      const keep = prev.ids.filter((id) => present.has(id));
      if (keep.length === prev.ids.length) return prev;
      return { ids: keep, anchor: null, focus: null, anchorID: null, focusID: keep.length ? prev.focusID : null };
    });
  }, [items]);

  // ---- sorting, selection and keys ----

  const sorted = useMemo(() => sortNodes(items, prefs.sort.key, prefs.sort.dir, lang), [items, prefs.sort.key, prefs.sort.dir, lang]);
  const ids = useMemo(() => sorted.map((n) => n.id), [sorted]);
  const byID = useMemo(() => new Map(sorted.map((n) => [n.id, n])), [sorted]);
  const selected = useMemo(() => new Set(sel.ids), [sel.ids]);
  const picked = sel.ids.map((id) => byID.get(id)).filter((n): n is Node => n !== undefined);
  const rovingID = sel.focusID && byID.has(sel.focusID) ? sel.focusID : (ids[0] ?? null);
  const view = prefs.view;
  const folderRole = here ? (here.dir ? (here.dir.role ?? "viewer") : here.folderRole) : "viewer";
  // Files can go into a directory by its ID, so a shared directory whose folder root is not reachable takes them.
  // Folders are made, and folder uploads placed, by path from the root, so those need a reachable path.
  const canUploadHere = here !== null && (here.dir !== null || here.path !== null) && atLeast(folderRole, "contributor");
  const canUploadFolder = canUploadHere && here !== null && here.path !== null;
  const canMakeFolder = canUploadFolder;
  const allEdit = picked.length > 0 && picked.every((n) => can.edit(n.role));
  // The space the listing is in: a visible one, or a hidden one, which is still reached.
  const space = here ? ([...folderList, ...hiddenList].find((f) => f.id === here.folder) ?? null) : null;
  // canShareItem says whether the caller may share an item here, from its role and the space's settings.
  const canShareItem = (n: Node): boolean =>
    shareCap({ role: n.role, editorsCanShare: n.editors_can_share, managersOnly: space?.sharing_managers_only ?? false }) !== null;
  const spaces = useMemo(
    () => (personalID ? [...folderList.filter((f) => f.id === personalID), ...folderList.filter((f) => f.id !== personalID)] : folderList),
    [folderList, personalID],
  );

  // gated shows an item as the no-download rule of its space makes it: a viewer of a space that forbids downloads
  // sees no download, whatever the item's own flag says. The server applies the same rule to every download.
  function gated(n: Node): Node {
    return here?.spaceNoDownload && !n.no_download ? { ...n, no_download: true } : n;
  }

  const crumbs = useMemo<Crumb<Place>[]>(() => {
    if (!here) return [];
    const rootLabel = here.folder === personalID ? t("nav.files") : (here.folderName ?? t("nav.files"));
    const out: Place[] = [];
    if (here.rootReachable) out.push({ key: `root-${here.folder}`, label: rootLabel, href: filesURL(here.folder, null) });
    for (const n of here.trail) out.push({ key: n.id, label: n.name, href: filesURL(here.folder, n.id) });
    if (here.dir) out.push({ key: here.dir.id, label: here.dir.name, href: filesURL(here.folder, here.dir.id) });
    return trimCrumbs(out, 4);
  }, [here, personalID, t]);

  // current is the selection with positions in the list as it is now.
  function current(): Selection {
    return {
      ids: sel.ids,
      anchor: sel.anchorID ? ids.indexOf(sel.anchorID) : null,
      focus: sel.focusID ? ids.indexOf(sel.focusID) : null,
    };
  }

  function setSelection(next: Selection) {
    setSel({
      ids: next.ids,
      anchor: next.anchor,
      focus: next.focus,
      anchorID: next.anchor !== null ? (ids[next.anchor] ?? null) : null,
      focusID: next.focus !== null ? (ids[next.focus] ?? null) : null,
    });
  }

  function onRowClick(index: number, e: Click) {
    setSelection(clickSelect(current(), ids, index, { shift: e.shiftKey, toggle: e.ctrlKey || e.metaKey }));
  }

  function onSelectAll(checked: boolean) {
    setSelection(checked ? selectAll(ids) : EMPTY);
  }

  // toggleStar stars or unstars an item for the caller, then shows the new state in the listing. A refused change
  // leaves the listing as it was and says why, unless the session has ended.
  async function toggleStar(n: Node): Promise<void> {
    const changed = sessionChanged();
    const next = n.starred !== true;
    try {
      if (next) await starNode(n.id);
      else await unstarNode(n.id);
      if (changed()) return;
      qc.setQueriesData<InfiniteData<Page<Node>>>({ queryKey: [sessionKey(get(session)), "children"] }, (data) =>
        data
          ? { ...data, pages: data.pages.map((pg) => ({ ...pg, items: pg.items.map((x) => (x.id === n.id ? { ...x, starred: next } : x)) })) }
          : data,
      );
    } catch (err) {
      if (!changed() && !signingOut()) setNotice(fileErrorText(err, lang));
    }
  }

  function openNode(n: Node) {
    if (n.kind === "shortcut") {
      // A reachable target opens at its own address; an unavailable one asks for access on the shortcut, and the
      // target's ID is shown nowhere.
      const got = openShortcut(n);
      if (got.kind === "go") {
        go(got.to);
      } else {
        setTarget(n);
        setDialog("unavailable");
      }
      return;
    }
    if (n.kind === "dir") {
      if (here) go(filesURL(here.folder, n.id));
      return;
    }
    if (previewable(n)) {
      openPreview(n);
    } else {
      setTarget(n);
      setDialog("details");
    }
  }

  function openPreview(n: Node) {
    const list = sorted.filter((x) => previewable(x));
    setPreviewItems(list.map(gated));
    setPreviewIndex(Math.max(0, list.findIndex((x) => x.id === n.id)));
    setDialog("preview");
  }

  function focusRow(id: string | null) {
    if (!id) return;
    requestAnimationFrame(() => {
      const el = Array.from(listRef.current?.querySelectorAll<HTMLElement>("[data-id]") ?? []).find((x) => x.dataset.id === id);
      el?.focus();
    });
  }

  // columns is how many tiles sit on one row, read from the layout. A list has one column.
  function columns(): number {
    if (view === "list" || !listRef.current) return 1;
    const tiles = Array.from(listRef.current.querySelectorAll<HTMLElement>("[data-id]"));
    if (tiles.length === 0) return 1;
    const top = tiles[0].offsetTop;
    const wrap = tiles.findIndex((x) => x.offsetTop !== top);
    return wrap === -1 ? tiles.length : wrap;
  }

  function onKey(e: ReactKeyboardEvent<HTMLElement>) {
    if (renameRef.current) return;
    const rtl = document.documentElement.dir === "rtl";
    const action = keyAction(e, { cols: columns(), grid: view === "grid", rtl, count: ids.length });
    if (!action) return;
    switch (action.kind) {
      case "move": {
        e.preventDefault();
        const next = moveFocus(current(), ids, action.delta, action.extend);
        setSelection(next);
        focusRow(ids[next.focus ?? 0] ?? null);
        return;
      }
      case "enter": {
        const n = sel.focusID ? byID.get(sel.focusID) : undefined;
        if (n) {
          e.preventDefault();
          openNode(n);
        }
        return;
      }
      case "space": {
        e.preventDefault();
        const idx = sel.focusID ? ids.indexOf(sel.focusID) : -1;
        if (idx >= 0) setSelection(clickSelect(current(), ids, idx, { toggle: true }));
        return;
      }
      case "f2": {
        const n = picked.length === 1 ? picked[0] : undefined;
        if (n && can.edit(n.role)) {
          e.preventDefault();
          startRename(n);
        }
        return;
      }
      case "delete":
        if (picked.length > 0 && allEdit) {
          e.preventDefault();
          askDelete(picked);
        }
        return;
      case "up":
        e.preventDefault();
        goUp();
        return;
      case "selectAll":
        e.preventDefault();
        setSelection(selectAll(ids));
        return;
      case "escape":
        setSelection(EMPTY);
        return;
    }
  }

  function goUp() {
    if (!here || !here.dir) return;
    const parent = here.trail.length > 0 ? here.trail[here.trail.length - 1].id : null;
    go(filesURL(here.folder, parent));
  }

  // ---- rename ----

  function startRename(n: Node) {
    setRename({ id: n.id, value: n.name, error: null });
  }

  // finishRename runs once per edit: a commit or a cancel, from Enter, Escape or leaving the field.
  async function finishRename(commit: boolean) {
    const r = renameRef.current;
    if (!r) return;
    setRename(null);
    const node = byID.get(r.id);
    if (!commit || !node) return;
    const value = r.value.trim();
    if (value === node.name) return;
    const problem = nameProblem(value);
    if (problem) {
      setRename({ id: r.id, value: r.value, error: t(nameMessageKey(problem)) });
      return;
    }
    const changed = sessionChanged();
    try {
      const after = await renameNode(node.id, value, node.etag);
      if (changed()) return;
      const before = node.name;
      const label = t("files.undo.renamed");
      undoStack.current?.push(label, async () => {
        let bad = 0;
        try {
          // The ETag from the rename is the one the undo must match: a change in between fails the undo.
          await renameNode(node.id, before, after.etag);
        } catch {
          bad = 1;
        }
        await reload();
        return bad;
      });
      showUndo(label);
      await reload();
    } catch (err) {
      if (changed()) return;
      setRename({ id: r.id, value: r.value, error: fileErrorText(err, lang) });
    }
  }

  // ---- delete ----

  function askDelete(nodes: Node[]) {
    setPending(nodes);
    setDialog("delete");
  }

  async function confirmDelete() {
    const nodes = pending;
    setDialog(null);
    const changed = sessionChanged();
    const done: Node[] = [];
    let failed = 0;
    for (const n of nodes) {
      try {
        await deleteNode(n.id);
        if (changed() || !mounted.current) return;
        done.push(n);
      } catch {
        if (changed() || !mounted.current) return;
        failed++;
      }
    }
    if (done.length > 0) {
      const label = t("files.undo.deleted");
      undoStack.current?.push(label, async () => {
        let bad = 0;
        for (const n of done) {
          try {
            await restoreNode(n.id);
          } catch {
            bad++;
          }
        }
        await reload();
        return bad;
      });
      showUndo(label);
    }
    if (failed > 0) setNotice(t("files.err.someFailed", { count: failed }));
    await reload();
  }

  // ---- move and copy ----

  function startPick(mode: "move" | "copy", nodes: Node[]) {
    setPickMode(mode);
    setPending(nodes);
    setDialog("pick");
  }

  // askConflict asks what to do with a taken name. The answer comes back when the person chooses.
  // A question still open when the page leaves is answered "skip", so no promise is left waiting.
  function askConflict(name: string, batch: boolean): Promise<{ choice: ConflictChoice; remember: boolean }> {
    setConflict({ name, batch });
    return new Promise((resolve) => {
      conflictResolve.current = resolve;
    });
  }

  function answerConflict(choice: ConflictChoice, remember: boolean) {
    const resolve = conflictResolve.current;
    setConflict(null);
    conflictResolve.current = null;
    resolve?.({ choice, remember });
  }

  async function runTransfer(mode: "move" | "copy", nodes: Node[], dest: Destination) {
    setDialog(null);
    const changed = sessionChanged();
    const moved: Array<{ node: Node; from: { folder: string; parent?: string; name: string } }> = [];
    let batch: ConflictChoice | null = null;
    let failed = 0;
    let jobsStarted = 0;
    const base: Transfer = dest.dir ? { folder: dest.folder, parent: dest.dir } : { folder: dest.folder, path: "/" };
    for (const n of nodes) {
      let choice: ConflictChoice | null = batch;
      for (;;) {
        const onConflict = choice === "replace" ? "replace" : choice === "rename" ? "rename" : "fail";
        try {
          if (choice === "skip") break;
          if (mode === "move") {
            const after = await moveNode(n.id, { ...base, on_conflict: onConflict }, n.etag);
            if (changed() || !mounted.current) return;
            moved.push({ node: after, from: { folder: n.folder_id, parent: n.parent_id ?? undefined, name: n.name } });
          } else {
            const res = await copyNode(n.id, { ...base, on_conflict: onConflict });
            if (changed() || !mounted.current) return;
            if ("status" in res) {
              jobsStarted++;
              watchJob(res.id);
            }
          }
          break;
        } catch (err) {
          if (changed() || !mounted.current) return;
          if (err instanceof ApiError && err.code === "conflict" && onConflict === "fail") {
            const answer = await askConflict(n.name, nodes.length > 1);
            if (changed() || !mounted.current) return;
            if (answer.remember) batch = answer.choice;
            choice = answer.choice;
            continue;
          }
          failed++;
          setNotice(fileErrorText(err, lang));
          break;
        }
      }
    }
    if (moved.length > 0) {
      const label = t("files.undo.moved");
      undoStack.current?.push(label, async () => {
        let bad = 0;
        for (const m of [...moved].reverse()) {
          try {
            await moveNode(m.node.id, { folder: m.from.folder, parent: m.from.parent, name: m.from.name }, m.node.etag);
          } catch {
            bad++;
          }
        }
        await reload();
        return bad;
      });
      showUndo(label);
    }
    if (failed > 0) setNotice(t("files.err.someFailed", { count: failed }));
    else if (mode === "copy" && jobsStarted === 0) setNotice(t("files.copy.done"));
    await reload();
  }

  // watchJob polls a copy that the server runs as a job, until it finishes.
  function watchJob(id: string) {
    setJobs((prev) => [...prev, { id, status: "queued" }]);
    const poll = async () => {
      if (!mounted.current) return;
      try {
        const j = await getJob(id);
        if (!mounted.current) return;
        setJobs((prev) => prev.map((x) => (x.id === id ? { id, status: j.status } : x)));
        if (j.status === "queued" || j.status === "running") setTimeout(() => void poll(), 1500);
        else if (j.status === "succeeded") await reload();
      } catch {
        if (mounted.current) setJobs((prev) => prev.map((x) => (x.id === id ? { id, status: "failed" } : x)));
      }
    };
    void poll();
  }

  // ---- undo ----

  function showUndo(label: string) {
    const top = undoStack.current?.current();
    if (!top) return;
    setUndoView({ label, expiresAt: top.expiresAt, status: "" });
  }

  function dismissUndo() {
    setUndoView(null);
    undoStack.current?.clear();
  }

  async function doUndo() {
    const entry = undoStack.current?.take();
    if (!entry) {
      dismissUndo();
      return;
    }
    const bad = await entry.run();
    setUndoView({
      label: entry.label,
      expiresAt: Date.now() + 4000,
      status: bad > 0 ? translatePlural(lang, "files.undo.partial", bad) : t("files.undo.done"),
    });
  }

  // ---- create folder ----

  function openNewFolder() {
    setNewName("");
    setNewNameError(null);
    setDialog("newFolder");
  }

  async function createFolder() {
    if (!here || here.path === null) return;
    const name = newName.trim();
    const problem = nameProblem(name);
    if (problem) {
      setNewNameError(t(nameMessageKey(problem)));
      return;
    }
    const folderPath = joinPath(here.path, name);
    if (!folderPath) return;
    const changed = sessionChanged();
    try {
      await mkdir(here.folder, folderPath);
      if (changed()) return;
      setDialog(null);
      setNotice(t("files.newFolder.created", { name }));
      await reload();
    } catch (err) {
      if (!changed()) setNewNameError(fileErrorText(err, lang));
    }
  }

  // ---- uploads ----

  function queueFiles(files: DroppedFile[]) {
    if (!here) return;
    const batch: Array<{ file: File; target: NonNullable<ReturnType<typeof uploadTargetFor>> }> = [];
    let skipped = 0;
    for (const d of files) {
      const tgt = uploadTargetFor(here, d.relDir);
      if (tgt) batch.push({ file: d.file, target: tgt });
      else skipped++;
    }
    if (batch.length > 0) manager.add(batch);
    if (skipped > 0) setNotice(t("files.upload.noPath"));
  }

  function onFilesPicked(e: ChangeEvent<HTMLInputElement>) {
    const input = e.currentTarget;
    if (input.files) queueFiles(fromFileList(input.files));
    input.value = "";
  }

  // insideBrowser is true for an event on this page's own elements. A dialog or the preview is a portal: React
  // passes its events up the component tree, so they reach this section, and their drops must not start uploads.
  function insideBrowser(e: DragEvent<HTMLElement>): boolean {
    const target = e.target;
    return target instanceof HTMLElement && sectionRef.current?.contains(target) === true;
  }

  async function onDrop(e: DragEvent<HTMLElement>) {
    if (!insideBrowser(e)) return;
    e.preventDefault();
    setDragging(false);
    dragDepth.current = 0;
    if (!e.dataTransfer || !canUploadHere) return;
    queueFiles(await fromDataTransfer(e.dataTransfer));
  }

  function onDragOver(e: DragEvent<HTMLElement>) {
    if (!insideBrowser(e)) return;
    if (!canUploadHere || !e.dataTransfer?.types.includes("Files")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
  }

  function onDragEnter(e: DragEvent<HTMLElement>) {
    if (!insideBrowser(e)) return;
    if (!canUploadHere || !e.dataTransfer?.types.includes("Files")) return;
    dragDepth.current++;
    setDragging(true);
  }

  function onDragLeave() {
    dragDepth.current = Math.max(0, dragDepth.current - 1);
    if (dragDepth.current === 0) setDragging(false);
  }

  // ---- other actions ----

  async function download(n: Node) {
    if (!downloadAllowed(n)) return;
    const ok = await startDownload(n.id).catch(() => false);
    if (!ok) setNotice(t("files.err.downloadRestricted"));
  }

  function changePrefs(next: ViewPrefs) {
    setPrefs(next);
    writePrefs(next);
  }

  function sortBy(key: SortKey) {
    changePrefs({ ...prefs, sort: nextSort(prefs.sort, key) });
  }

  function reverseSort() {
    const dir: SortDir = prefs.sort.dir === "asc" ? "desc" : "asc";
    changePrefs({ ...prefs, sort: { key: prefs.sort.key, dir } });
  }

  // ---- render ----


  if (route === null || route.kind === "node" || place?.kind === "missing") {
    // A directory address that is not there offers an access request for it, as the Svelte page did.
    return <NotFoundPage nodeID={route?.kind === "dir" ? route.dir : undefined} />;
  }
  if (placeQ.isError) {
    return <Alert type="error" showIcon role="alert" message={fileErrorText(placeQ.error, lang)} />;
  }
  if (!here) {
    return (
      <div role="status" aria-busy="true">
        <span className="sr-only">{t("files.loading")}</span>
        <Skeleton active paragraph={{ rows: 6 }} />
      </div>
    );
  }

  const titleName = here.dir?.name ?? here.folderName ?? t("nav.files");
  const selectedNodes = picked;
  const rowLabel = titleName;

  return (
    <section
      ref={sectionRef}
      className="browser"
      aria-labelledby="browser-title"
      onDragEnter={onDragEnter}
      onDragLeave={onDragLeave}
      onDragOver={onDragOver}
      onDrop={onDrop}
      // The rows keep their own height: a short folder must not stretch the header and the toolbar apart.
      style={{ position: "relative", display: "grid", gap: 16, minWidth: 0, minHeight: "60dvh", alignContent: "start" }}
    >
      <header style={{ minWidth: 0 }}>
        <Breadcrumbs crumbs={crumbs} />
        <h1 id="browser-title" className="sr-only">
          {titleName}
        </h1>
      </header>

      {childrenQ.isPending ? (
        <div role="status" aria-busy="true">
          <span className="sr-only">{t("files.loading")}</span>
          <Skeleton active paragraph={{ rows: 6 }} />
        </div>
      ) : childrenQ.isError && childrenQ.data === undefined ? (
        <div role="alert">
          <Alert type="error" showIcon message={fileErrorText(childrenQ.error, lang)} />
          <Button onClick={() => void childrenQ.refetch()} style={{ marginTop: 12 }}>
            {t("files.retry")}
          </Button>
        </div>
      ) : (
        <>
          {notice && <Alert type="info" showIcon role="status" message={notice} closable onClose={() => setNotice(null)} />}
          {readFailure && <Alert type="info" showIcon role="status" message={readFailure} />}

          <div role="toolbar" aria-label={t("files.toolbar")} style={toolbarStyle}>
            {canMakeFolder && <Button onClick={openNewFolder}>{t("files.act.newFolder")}</Button>}
            {canUploadHere && (
              <>
                <Button type="primary" onClick={() => fileInputRef.current?.click()}>
                  {t("files.act.upload")}
                </Button>
                {canUploadFolder && <Button onClick={() => folderInputRef.current?.click()}>{t("files.act.uploadFolder")}</Button>}
                <input ref={fileInputRef} type="file" multiple hidden tabIndex={-1} aria-hidden="true" onChange={onFilesPicked} />
                <input
                  ref={(el) => {
                    folderInputRef.current = el;
                    // The folder picker takes directories; the attribute is not in the typed attribute list.
                    el?.setAttribute("webkitdirectory", "");
                  }}
                  type="file"
                  multiple
                  hidden
                  tabIndex={-1}
                  aria-hidden="true"
                  onChange={onFilesPicked}
                />
              </>
            )}
            <span style={{ flex: "1 1 auto" }} />
            <div role="group" aria-label={t("files.view.label")} style={{ display: "inline-flex", gap: 4 }}>
              <Button aria-pressed={view === "list"} type={view === "list" ? "primary" : "default"} onClick={() => changePrefs({ ...prefs, view: "list" })}>
                {t("files.view.list")}
              </Button>
              <Button aria-pressed={view === "grid"} type={view === "grid" ? "primary" : "default"} onClick={() => changePrefs({ ...prefs, view: "grid" })}>
                {t("files.view.grid")}
              </Button>
            </div>
            {view === "grid" && (
              <>
                <Select
                  aria-label={t("files.sort.label")}
                  value={prefs.sort.key}
                  onChange={(v: SortKey) => sortBy(v)}
                  style={{ minWidth: 140 }}
                  options={[
                    { value: "name", label: t("files.sort.name") },
                    { value: "modified", label: t("files.sort.modified") },
                    { value: "size", label: t("files.sort.size") },
                  ]}
                />
                <Button onClick={reverseSort}>{t("files.sort.toggle")}</Button>
              </>
            )}
          </div>

          {sel.ids.length > 0 && (
            <div role="toolbar" aria-label={t("files.selection.label")} style={{ ...toolbarStyle, padding: "8px 12px", background: token.colorPrimaryBg, borderRadius: token.borderRadius }}>
              <p aria-live="polite" style={{ margin: "0 8px 0 0", fontWeight: 600 }}>
                {translatePlural(lang, "files.selected", sel.ids.length)}
              </p>
              {selectedNodes.length === 1 && selectedNodes[0].kind === "file" && downloadAllowed(gated(selectedNodes[0])) && (
                <Button onClick={() => void download(selectedNodes[0])}>{t("files.act.download")}</Button>
              )}
              {selectedNodes.length === 1 && previewable(selectedNodes[0]) && <Button onClick={() => openPreview(selectedNodes[0])}>{t("files.act.preview")}</Button>}
              {selectedNodes.length === 1 && (
                <Button
                  onClick={() => {
                    setTarget(selectedNodes[0]);
                    setDialog("details");
                  }}
                >
                  {t("files.act.details")}
                </Button>
              )}
              {selectedNodes.length === 1 && (
                <Button
                  onClick={() => {
                    setTarget(selectedNodes[0]);
                    setDialog("share");
                  }}
                >
                  {t("share.open")}
                </Button>
              )}
              {selectedNodes.length === 1 && selectedNodes[0].kind === "file" && atLeast(selectedNodes[0].role, "viewer") && (
                <Button
                  onClick={() => {
                    setTarget(selectedNodes[0]);
                    setDialog("versions");
                  }}
                >
                  {t("files.act.versions")}
                </Button>
              )}
              {selectedNodes.length === 1 && can.edit(selectedNodes[0].role) && <Button onClick={() => startRename(selectedNodes[0])}>{t("files.act.rename")}</Button>}
              {selectedNodes.length === 1 && selectedNodes[0].kind !== "shortcut" && spaces.length > 0 && (
                <Button
                  onClick={() => {
                    setTarget(selectedNodes[0]);
                    setDialog("pickShortcut");
                  }}
                >
                  {t("files.act.shortcut")}
                </Button>
              )}
              {allEdit && <Button onClick={() => startPick("move", selectedNodes)}>{t("files.act.move")}</Button>}
              {selectedNodes.length > 0 && <Button onClick={() => startPick("copy", selectedNodes)}>{t("files.act.copy")}</Button>}
              {allEdit && (
                <Button danger onClick={() => askDelete(selectedNodes)}>
                  {t("files.act.delete")}
                </Button>
              )}
              <Button onClick={() => setSelection(EMPTY)}>{t("files.act.clear")}</Button>
            </div>
          )}

          <div>
            {sorted.length === 0 ? (
              <p role="status" style={{ padding: "32px 16px", textAlign: "center", color: token.colorTextSecondary, border: `1px dashed ${token.colorBorder}`, borderRadius: token.borderRadiusLG }}>
                {t("files.empty")}
              </p>
            ) : (
              <>
                <Listing
                  items={sorted}
                  view={view}
                  selected={selected}
                  focusID={rovingID}
                  sortKey={prefs.sort.key}
                  sortDir={prefs.sort.dir}
                  renameID={rename?.id ?? null}
                  renameValue={rename?.value ?? ""}
                  renameError={rename?.error ?? null}
                  label={rowLabel}
                  onRowClick={onRowClick}
                  onRowDoubleClick={(i) => {
                    const n = sorted[i];
                    if (n) openNode(n);
                  }}
                  onSelectAll={onSelectAll}
                  onSort={sortBy}
                  onRenameChange={(v) => {
                    if (renameRef.current) setRename({ ...renameRef.current, value: v });
                  }}
                  onRename={(commit) => void finishRename(commit)}
                  onKeyDown={onKey}
                  onFocusItem={(i) => {
                    const id = ids[i];
                    if (id) setSel((prev) => ({ ...prev, focusID: id }));
                  }}
                  onStar={(n) => void toggleStar(n)}
                  containerRef={listRef}
                />
                {hasMore && (
                  <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 8, paddingBlock: 16 }}>
                    <Button onClick={() => void loadMore()} disabled={childrenQ.isFetchingNextPage}>
                      {childrenQ.isFetchingNextPage ? t("files.loadingMore") : t("files.loadMore")}
                    </Button>
                    <p style={{ margin: 0, fontSize: token.fontSizeSM, color: token.colorTextSecondary }}>{t("files.sortNote")}</p>
                  </div>
                )}
                {hasMore && <div ref={sentinelRef} aria-hidden="true" style={{ height: 1 }} />}
              </>
            )}
          </div>

          {jobs.length > 0 && (
            <ul aria-label={t("files.jobs")} style={{ margin: 0, padding: 0, listStyle: "none", fontSize: token.fontSizeSM }}>
              {jobs.map((j) => (
                <li key={j.id}>{j.status === "succeeded" ? t("files.job.done") : j.status === "failed" ? t("files.job.failed") : t("files.job.running")}</li>
              ))}
            </ul>
          )}

          {dragging && canUploadHere && (
            <div
              aria-hidden="true"
              style={{
                position: "fixed",
                inset: 16,
                zIndex: 20,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: token.fontSizeLG,
                fontWeight: 600,
                color: token.colorPrimary,
                background: token.colorPrimaryBg,
                border: `2px dashed ${token.colorPrimary}`,
                borderRadius: token.borderRadiusLG,
                pointerEvents: "none",
              }}
            >
              {t("files.drop.here")}
            </div>
          )}
        </>
      )}

      <Modal
        open={dialog === "newFolder"}
        title={t("files.newFolder.title")}
        okText={t("files.newFolder.submit")}
        cancelText={t("common.cancel")}
        onCancel={() => setDialog(null)}
        onOk={() => void createFolder()}
        maskClosable={false}
        destroyOnClose
        afterOpenChange={(open) => {
          if (open) document.getElementById("newfolder-name")?.focus();
        }}
      >
        <Form layout="vertical" onFinish={() => void createFolder()}>
          <Form.Item
            label={t("files.newFolder.label")}
            validateStatus={newNameError ? "error" : undefined}
            help={newNameError ?? undefined}
          >
            <Input id="newfolder-name" value={newName} onChange={(e) => setNewName(e.target.value)} aria-invalid={newNameError ? true : undefined} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={dialog === "delete"}
        title={t("files.delete.title")}
        okText={t("files.delete.submit")}
        okButtonProps={{ danger: true }}
        cancelText={t("common.cancel")}
        onCancel={() => setDialog(null)}
        onOk={() => void confirmDelete()}
        maskClosable={false}
      >
        <FocusOnOpen selector=".ant-modal-footer .ant-btn-dangerous" />
        <p>{translatePlural(lang, "files.delete.body", pending.length)}</p>
      </Modal>

      {dialog === "pick" && (
        <PickDestination mode={pickMode} spaces={spaces} onPick={(dest) => void runTransfer(pickMode, pending, dest)} onClose={() => setDialog(null)} />
      )}

      {dialog === "pickShortcut" && (
        <PickDestination
          mode="shortcut"
          spaces={spaces}
          onPick={(dest) => {
            setShortcutDest(dest);
            setDialog("shortcut");
          }}
          onClose={() => setDialog(null)}
        />
      )}

      {dialog === "shortcut" && target && shortcutDest && (
        <AddShortcut
          target={target}
          dest={shortcutDest}
          onDone={(made) => {
            setDialog(null);
            setNotice(t("files.shortcut.created", { name: made.name }));
            void reload();
          }}
          onClose={() => setDialog(null)}
        />
      )}

      {dialog === "unavailable" && target && (
        <Modal open title={t("shortcut.dialog")} footer={null} onCancel={() => setDialog(null)} maskClosable={false} destroyOnHidden>
          <p>{t("shortcut.unavailableBody")}</p>
          <RequestAccess nodeID={target.id} />
        </Modal>
      )}

      {dialog === "details" && target && (
        <Details
          node={target}
          canVersions={atLeast(target.role, "viewer")}
          canShare={canShareItem(target)}
          onVersions={() => setDialog("versions")}
          onShare={() => setDialog("share")}
          onClose={() => setDialog(null)}
          onChanged={(next) => {
            // A save answered late must not change another item: it applies only to the item the drawer shows now.
            setTarget((cur) => keepTargetOnSave(cur, next, dialogRef.current === "details"));
            void reload();
          }}
        />
      )}

      {dialog === "share" && target && (
        <Suspense fallback={null}>
          <ShareDialog node={target} space={space} onclose={() => setDialog(null)} onchanged={() => void reload()} />
        </Suspense>
      )}

      {dialog === "versions" && target && <Versions node={gated(target)} onChanged={() => void reload()} onClose={() => setDialog(null)} />}

      {dialog === "preview" && previewItems.length > 0 && (
        <Preview items={previewItems} index={previewIndex} onIndex={(i) => setPreviewIndex(i)} onClose={() => setDialog(null)} />
      )}

      {conflict && (
        <Conflict name={conflict.name} batch={conflict.batch} onAnswer={answerConflict} onCancel={() => answerConflict("skip", false)} />
      )}

      {undoView && <UndoToast label={undoView.label} expiresAt={undoView.expiresAt} status={undoView.status} onUndo={() => void doUndo()} onExpire={dismissUndo} />}
    </section>
  );
}
