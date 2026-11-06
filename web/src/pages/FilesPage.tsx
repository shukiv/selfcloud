// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { App, Alert, Button, ConfigProvider, Dropdown, Modal, Skeleton, theme as antdTheme } from "antd";
import IconButton from "../components/IconButton";
import SelectionBar from "../components/files/SelectionBar";
import {
  ClipboardPaste,
  Copy,
  CopyPlus,
  Download,
  Eye,
  FileArchive,
  FolderOpen,
  FolderInput,
  BookOpen,
  FolderPlus,
  FolderUp,
  History,
  ArrowUpDown,
  Info,
  LayoutGrid,
  Link as LinkIcon,
  Link2,
  List,
  EllipsisVertical,
  Pencil,
  Play,
  Plus,
  Scissors,
  Share2,
  Star,
  Trash2,
  Upload,
  FileSpreadsheet,
  FileText,
  Presentation,
} from "lucide-react";
import { useLang, useT, translatePlural } from "../lib/i18n";
import { useStore, get } from "../lib/store";
import { brand } from "../lib/brand";
import { ApiError } from "../lib/apiError";
import { go } from "../lib/router";
import { session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { queryKey, refreshTree, type Branch } from "../lib/query";
import { folders, hiddenFolders, loadFolders, loadHiddenFolders } from "../lib/files/folders";
import { keepTargetOnSave } from "../lib/files/appearance";
import { folderPageReader } from "../lib/files/folderPages";
import { focusAfterClose } from "../lib/files/focusAfterClose";
import { childrenPage, copyNode, deleteNode, getJob, getMe, moveNode, renameNode, restoreNode, serverLimits, statOrNull, type Transfer } from "../lib/files/api";
import { locate, type Here } from "../lib/files/location";
import { starNode, unstarNode } from "../lib/stars";
import { fileErrorText } from "../lib/files/fileErrors";
import { makeZip, type ZipPhase } from "../lib/files/zips";
import { officeConvert } from "../lib/files/convert";
import { editorEntry, editorPath, officeEdit } from "../lib/files/editor";
import { nameProblem, nameMessageKey, fromFileList, fromDataTransfer, startDownload, type DroppedFile } from "../lib/files/ops";
import { documentKinds, newPlace, placeOfHere, queueInto } from "../lib/files/newPlace";
import { useNewDocument } from "../lib/files/documents";
import { useSidebarDocked } from "../lib/sidebarDocked";
import { refreshMyStorage } from "../lib/storage/api";
import { parseFilesPath, filesURL, type FilesRoute } from "../lib/files/route";
import { trimCrumbs, type Crumb } from "../lib/files/breadcrumbs";
import { nextSort, type SortDir, type SortKey } from "../lib/files/sort";
import { clickSelect, moveFocus, selectAll, EMPTY, type Selection } from "../lib/files/selection";
import { isTextField, keyAction } from "../lib/files/keys";
import { clearClip, clipboard, cutIDs, setClip } from "../lib/files/clipboard";
import { trayRoom } from "../lib/files/trayRoom";
import { can, downloadAllowed, atLeast } from "../lib/files/roles";
import { shareCap } from "../lib/sharing/roles";
import { kindOf, previewable } from "../lib/files/kind";
import { tapOpens } from "../lib/files/tap";
import { readPrefs, writePrefs, type ViewPrefs } from "../lib/files/viewPrefs";
import { UndoStack } from "../lib/files/undo";
import { copyText, nodeAddress } from "../lib/files/copyText";
import { alreadyIn, dropDecision, type Dest } from "../lib/files/drag";
import { HEADER_H, rowPad, stickyTop, toolbarH } from "../lib/files/layout";
import { treePlace } from "../lib/files/treePlace";
import { rowStep, type TilePlace } from "../lib/files/grid";
import type { ConflictChoice } from "../lib/files/conflict";
import type { Job, Node, Page } from "../lib/files/types";
import { manager, uploads } from "../lib/files/uploads";
import Breadcrumbs, { type Place } from "../components/files/Breadcrumbs";
import Listing, { type Click, type RowAct, useCoarsePointer } from "../components/files/Listing";
import { groupMenu, type MenuSection } from "../lib/files/menuGroups";
import { isMacPlatform, shortcutHint, shortcutKeys } from "../lib/files/shortcutHint";
import { useGestures, type GestureCtl } from "../components/files/useGestures";
import Details from "../components/files/Details";
import Preview, { type MediaSeed } from "../components/files/Preview";
import { Pager } from "../lib/files/pager";
import { isPlayable } from "../lib/files/queue";
import Versions from "../components/files/Versions";
import PickDestination, { type Destination } from "../components/files/PickDestination";
import AddShortcut from "../components/files/AddShortcut";
import RequestAccess from "../components/share/RequestAccess";
import { openShortcut } from "../lib/files/shortcut";
import Conflict from "../components/files/Conflict";
import FocusOnOpen from "../components/FocusOnOpen";
import UndoToast from "../components/UndoToast";
import NotFoundPage from "./NotFoundPage";
import StorageNotice from "../components/storage/StorageNotice";

// The file browser: a folder or a directory, its items, and what the signed-in person may do with them.
// The server checks every operation again; the buttons only show what the role allows.
//
// The share dialog is loaded on first use: it is not part of the file browser's first load.
const ShareDialog = lazy(() => import("../components/share/ShareDialog"));
// The zip dialog is loaded on first use, as the share dialog is.
const ZipDialog = lazy(() => import("../components/files/ZipDialog"));
const NewFolderDialog = lazy(() => import("../components/files/NewFolderDialog"));
// The folder tree is loaded when it is first shown, and not at all at phone width.

// Sharing from this page: the Share button in the selection bar (when the caller may share the item), Share and
// "Who has access" in Details, and the access request on a not-found directory.

type Dialog = "newFolder" | "delete" | "pick" | "details" | "versions" | "preview" | "share" | "pickShortcut" | "shortcut" | "unavailable" | "zip" | null;

// previewKey names the action that shows a file: Play for a video or audio file, View for a picture, Preview for the rest.
function previewKey(n: Node): "files.act.play" | "files.act.view" | "files.act.preview" {
  const k = kindOf(n);
  if (k === "video" || k === "audio") return "files.act.play";
  if (k === "image") return "files.act.view";
  return "files.act.preview";
}

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

// A menu that is taller than the room above and below the pointer shifts along the window as well as flipping (antd shifts only
// the placements top, bottom, left and right, and the menus here use bottomLeft), and when it is taller than the window it scrolls
// inside it, so every item can be reached.
const MENU_FIT = { adjustX: 1, adjustY: 1, shiftX: true, shiftY: true } as const;
const MENU_SCROLL = { maxBlockSize: "calc(100dvh - 16px)", overflowY: "auto" } as const;
// antd offsets a menu 4 px from its anchor after it has been fitted to the window; 4 px of padding in the popup keeps the offset menu inside.
const MENU_POPUP = { paddingBlock: 4 } as const;
// MAC_KEYS says whether the shortcuts a menu shows are the Mac's (the command sign) or the Windows and Linux ones (Ctrl).
const MAC_KEYS = typeof navigator !== "undefined" && isMacPlatform(navigator);

// The keys that would have acted on the folder left behind while the next one loads. Each gets a short notice, once per
// busy period (decision 80). Arrow keys, Tab and Escape do nothing and say nothing.
const BUSY_NOTICE_KEYS: ReadonlySet<string> = new Set(["copy", "cut", "paste", "delete", "f2", "selectAll", "enter"]);


// useRoomy says whether the window is at least 360 pixels wide. Below it the selection panel keeps two icons, and the rest go in
// its More menu, so the panel never runs past the window's edge; a window that cannot say its width is taken as roomy.
function useRoomy(min = 360): boolean {
  const query = `(min-width: ${min}px)`;
  const supported = typeof window !== "undefined" && typeof window.matchMedia === "function";
  const [roomy, setRoomy] = useState(() => (supported ? window.matchMedia(query).matches : true));
  useEffect(() => {
    if (!supported) return;
    const mq = window.matchMedia(query);
    const onChange = () => setRoomy(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [supported]);
  return roomy;
}

// BarAct is one action: its label and icon, what it does, and whether it destroys something. A hidden action is one the
// caller may never perform here, or one that does not fit the selection's kind, so it is not shown at all; a disabled one
// needs a selection that is not there now. group orders the menus: a divider separates the groups.
interface BarAct {
  key: string;
  label: string;
  // menuLabel is the entry's text in a menu, where it differs from the toolbar's (Delete is Move to trash in the menu).
  menuLabel?: string;
  icon: ReactNode;
  group: number;
  // section is the menu part the action sits in (lib/files/menuGroups.ts).
  section: MenuSection;
  // fits is false when the selection is of a kind the action does not apply to; a menu leaves such an action out.
  fits: boolean;
  run: () => void;
  danger?: boolean;
  hidden: boolean;
  enabled: boolean;
}

// MenuAt is the right-click menu: where it opens, and whether it offers the item's actions or the place's. The place is
// logical (inline from the start edge, block from the top), so it is right in a right-to-left page too.
interface MenuAt {
  inline: number;
  block: number;
  scope: "selection" | "space";
}

// menuAt turns a pointer's place in the window into the logical place of the menu, measured from the start edge of the page.
function menuAt(x: number, y: number, scope: MenuAt["scope"]): MenuAt {
  const rtl = document.documentElement.dir === "rtl";
  return { inline: rtl ? window.innerWidth - x : x, block: y, scope };
}

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

// dialogOpen says whether a dialog is on screen. antd keeps a closed modal in the DOM (its wrapper is display none) and a
// closed drawer too (it loses the open class), so a plain query for the role finds a dialog that is not there.
function dialogOpen(): boolean {
  return Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]')).some((d) => {
    const modal = d.closest<HTMLElement>(".ant-modal-wrap");
    if (modal) return getComputedStyle(modal).display !== "none";
    const drawer = d.closest<HTMLElement>(".ant-drawer");
    if (drawer) return drawer.classList.contains("ant-drawer-open");
    return getComputedStyle(d).display !== "none" && getComputedStyle(d).visibility !== "hidden";
  });
}

export default function FilesPage() {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  const coarse = useCoarsePointer();
  const roomy = useRoomy();
  const spacious = useRoomy(480);
  // Where the sidebar is shown, its New button holds the New actions, so the toolbar has none. A drawer has no New of its own,
  // so the toolbar keeps its New there (decision 66).
  const docked = useSidebarDocked();
  // Action notices are toasts: a toast covers no row, where an alert above the listing pushed every row down.
  const { message } = App.useApp();
  const qc = useQueryClient();
  const location = useLocation();
  const path = location.pathname;
  const search = location.search;
  const route = useMemo(() => parseFilesPath(path), [path]);
  const brandName = useStore(brand).name;
  const folderList = useStore(folders);
  const hiddenList = useStore(hiddenFolders);
  const uploadList = useStore(uploads);
  const clip = useStore(clipboard);
  // officeOn is true while the server offers document previews (decision 59). Then an office document opens in the viewer.
  const officeOn = useStore(officeConvert);
  // editorOn is true while the server offers the office editor (decision 72): the menu then offers Open in editor or Open in viewer.
  const editorOn = useStore(officeEdit);
  // The toolbar's New and the drawer's make blank documents as the sidebar's New does (decision 91).
  const makeDocument = useNewDocument();
  // viewable is a file the viewer opens: a previewable one, or an office document while its preview is offered.
  const viewable = (n: Node) => previewable(n) || (officeOn && kindOf(n) === "office");
  // room is the space the upload tray covers at the bottom of the page: the last rows are scrolled clear of it.
  const room = useStore(trayRoom);
  // menu is the right-click menu while it is open (see onRowContext and onSpaceContext).
  const [menu, setMenu] = useState<MenuAt | null>(null);
  // menuFromKey says the menu was opened from the keyboard, so it takes the focus; menuOrigin is the row it returns to when it
  // closes unchosen; menuChosen says an item was picked, so the focus goes to what the item opened instead.
  const menuFromKey = useRef(false);
  const menuOrigin = useRef<HTMLElement | null>(null);
  const menuChosen = useRef(false);
  // pasting says a paste is running, so a second paste (a held key, a double press) waits for it rather than repeating it.
  const pasting = useRef(false);
  // toolbarFocus says the focus is in the toolbar, so a button disabled under it hands the focus to the listing.
  const toolbarFocus = useRef(false);

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
  // The pointer gestures: an item dragged onto a folder, and the rubber band. What they may do is answered by gestureCtl below,
  // which is set at each render, so a gesture always asks the page as it is now.
  const gestureCtl = useRef<GestureCtl | null>(null);
  const gestures = useGestures(sectionRef, listRef, gestureCtl);

  const [rename, setRenameState] = useState<RenameState | null>(null);
  const renameRef = useRef<RenameState | null>(null);
  const setRename = (next: RenameState | null) => {
    renameRef.current = next;
    setRenameState(next);
  };

  const [dialog, setDialog] = useState<Dialog>(null);
  // zip is what the zip dialog shows, and zipStop stops watching the zip. The page starts a zip, and stops it when the
  // dialog goes: closing the dialog, or a place change, ends the watch, so no download starts behind the person's back.
  const [zip, setZip] = useState<ZipPhase | null>(null);
  const zipStop = useRef<(() => void) | null>(null);
  useEffect(() => {
    if (dialog === "zip") return;
    zipStop.current?.();
    zipStop.current = null;
    setZip(null);
  }, [dialog]);
  useEffect(() => () => zipStop.current?.(), []);
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
    // The clipboard is emptied by lib/files/clipboard when the person changes, wherever the page is.
    setDialog((d) => (d === "share" ? null : d));
  }, [sessionID]);
  const [pending, setPending] = useState<Node[]>([]);
  const [previewItems, setPreviewItems] = useState<Node[]>([]);
  const [previewIndex, setPreviewIndex] = useState(0);
  // previewMedia is the playlist a video or audio file opened (or the selection played), with the folder's files to read on.
  const [previewMedia, setPreviewMedia] = useState<MediaSeed | null>(null);
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
    // While the next place is looked up, the page keeps the place it showed: its listing stays on screen, busy and inert, and
    // the loading notice does not replace it (decision 77, item fu3b). The first place of the page has none, and shows the notice.
    placeholderData: keepPreviousData,
  });
  const place = placeQ.data;
  const placeBusy = placeQ.isPlaceholderData;
  const here = place?.kind === "ready" ? place.here : null;
  const personalID = place?.kind === "ready" ? place.personalID : null;
  // The sidebar's folder tree reads the place shown (treePlace). It is written each time the place changes, and it is kept
  // when the page is left, so the tree still knows a space's top folder on the other pages.
  const treeChain = here ? (here.rootReachable ? [...here.trail.map((n) => n.id), ...(here.dir ? [here.dir.id] : [])] : here.reach.map((n) => n.id)) : [];
  const treeTop = here && !here.rootReachable && here.reach.length > 0 ? { id: here.reach[0].id, name: here.reach[0].name, role: here.reach[0].role } : undefined;
  const treeKeyText = here ? [here.folder, ...treeChain, treeTop?.id ?? ""].join("|") : "";
  useEffect(() => {
    if (!here) return;
    treePlace.set({ folder: here.folder, chain: treeChain, top: treeTop });
    // The effect runs for a new place only: its key is the text of the place.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [treeKeyText]);
  const folderID = here?.folder ?? null;
  const dirID = here?.dir?.id ?? null;
  // A press on Play media is answered only while the person is still at the address it was pressed at, with the sort it was
  // pressed under, and until the person opens something else. The address moves at once, ahead of the place being read.
  const playAsk = useRef(0);
  useEffect(
    () => () => {
      playAsk.current++;
    },
    [location.pathname, prefs.sort.key, prefs.sort.dir],
  );

  const childrenQ = useInfiniteQuery({
    // The sort is part of the key, so a sort change reads the directory again from its first page.
    queryKey: queryKey("children", folderID, dirID, prefs.sort.key, prefs.sort.dir),
    refetchOnMount: "always",
    queryFn: ({ pageParam }: { pageParam: string | undefined }) =>
      childrenPage(folderID as string, dirID, pageParam, prefs.sort),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<Node>) => last.next_cursor,
    enabled: signedIn && here !== null,
  });
  const fetched = useMemo<Node[]>(() => (childrenQ.data?.pages ?? []).flatMap((p) => p.items), [childrenQ.data]);
  // The rows of the last read that worked in this folder. A failed first read after a sort change keeps them on screen,
  // with the failure notice. A folder's first read that fails has none, and shows the error.
  const placeKey = `${folderID}/${dirID}`;
  const lastRows = useRef<{ place: string; items: Node[] } | null>(null);
  useEffect(() => {
    if (childrenQ.data) lastRows.current = { place: placeKey, items: fetched };
  }, [childrenQ.data, fetched, placeKey]);
  const kept = childrenQ.isError && childrenQ.data === undefined && lastRows.current?.place === placeKey ? lastRows.current.items : null;
  // While the next place (or sort) is read, the rows on screen are the last ones read, busy and inert, so no frame is left
  // without a listing (decision 77, item fu3b). The first read of the page has none, and shows the loading notice.
  const stale = childrenQ.isPending && lastRows.current !== null ? lastRows.current.items : null;
  const busy = placeBusy || stale !== null;
  // The notice for a key pressed while busy is shown once per busy period (fu4, decision 80).
  const busyNoticed = useRef(false);
  // The same text is held in a polite live region, which assistive technology reads (antd's message is not one; fu4 review 1).
  const [busyNote, setBusyNote] = useState("");
  useEffect(() => {
    if (!busy) {
      busyNoticed.current = false;
      setBusyNote("");
    }
  }, [busy]);
  const items = kept ?? stale ?? fetched;
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
    setMenu(null);
  }, [path]);

  // A menu opened from the keyboard takes the focus: its list is focused, so the arrow keys move in it.
  useEffect(() => {
    if (!menu || !menuFromKey.current) return;
    menuFromKey.current = false;
    // The first item takes the focus, so an arrow key moves on from it (review fix 1); the menu's list is the fallback.
    const frame = requestAnimationFrame(() => {
      const first = document.querySelector<HTMLElement>(".ant-dropdown:not(.ant-dropdown-hidden) [role='menuitem']:not(.ant-dropdown-menu-item-disabled)");
      (first ?? document.querySelector<HTMLElement>(".ant-dropdown:not(.ant-dropdown-hidden) [role='menu']"))?.focus();
    });
    return () => cancelAnimationFrame(frame);
  }, [menu]);

  // A toolbar button disabled while it has the focus (Clear, or Delete once its items are gone) takes the focus with it, to the
  // body. The focus goes to the listing's current row instead.
  useEffect(() => {
    // Focus left in a dialog (the delete's own button, which stays in the DOM when it closes) counts as lost too.
    const active = document.activeElement;
    const inDialog = !!active?.closest(".ant-modal-wrap");
    if (!toolbarFocus.current || (active && active !== document.body && !inDialog)) return;
    toolbarFocus.current = false;
    // The current row may be gone (a delete), so the first row takes the focus then.
    (listRef.current?.querySelector<HTMLElement>("[data-id][tabindex='0']") ?? listRef.current?.querySelector<HTMLElement>("[data-id]"))?.focus();
  }, [sel.ids.length]);

  // Keys go to the page, not only to a row: Ctrl+C, X and V, and Backspace or Alt+Up, work wherever the focus is in the
  // tab. The listener reads the page as of the last render, through a ref, so it never acts on an old selection.
  const onShortcutRef = useRef<(e: KeyboardEvent) => void>(() => undefined);
  onShortcutRef.current = onShortcut;
  useEffect(() => {
    const listen = (e: KeyboardEvent) => onShortcutRef.current(e);
    window.addEventListener("keydown", listen);
    return () => window.removeEventListener("keydown", listen);
  }, []);

  // Redirect /files to the caller's own folder, unless the person has already gone elsewhere: right after signing in the
  // read of the person is still going on, and a click made meanwhile moves the address before this page is replaced.
  // It runs again on every navigation: My files pressed during the move comes back to /files with the place still the
  // redirect, and nothing else would move it on, so the page would stay empty.
  useEffect(() => {
    if (place?.kind === "redirect" && window.location.pathname === "/files") go(place.to, { replace: true });
  }, [place, location.key]);

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
    // A notification's address asks for the file's details, which hold its comments (decision 55).
    else if (q.get("details") === "comments") {
      setTarget(found);
      setDialog("details");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, search, path, here, childrenQ.isPending]);

  // reload reads the listing again, keeping the place. Selected items that are gone are dropped.
  // The folder tree is read again only for the folder shown and the ones the change named (where items left or came to).
  function reload(also: Branch[] = []): Promise<unknown> {
    const shown: Branch[] = folderID ? [{ folder: folderID, dir: dirID }] : [];
    // A change that moves bytes (a delete, a move to the trash, an upload that ends) also reads the sidebar's storage meter.
    void refreshMyStorage().catch(() => undefined);
    return refreshTree(qc, [...shown, ...also]);
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
  const readFailure = childrenQ.isError && (childrenQ.data !== undefined || kept !== null) ? fileErrorText(childrenQ.error, lang) : null;

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

  // While the rows on screen are the folder left behind, nothing is selected in them: the selection is cleared as the change
  // starts (fu3b review 1). Focus that was in the rows is put in the next listing once it loads (fu3b review 4).
  useEffect(() => {
    if (busy) setSel(NO_SEL);
  }, [busy]);
  const listWrap = useRef<HTMLDivElement | null>(null);
  const listFocused = useRef(false);
  useEffect(() => {
    if (busy || !listFocused.current) return;
    listFocused.current = false;
    const active = document.activeElement;
    if (active && active !== document.body) return;
    const first = listWrap.current?.querySelector<HTMLElement>('[role="row"]');
    (first ?? listWrap.current)?.focus();
  }, [busy]);

  // ---- sorting, selection and keys ----

  // The server sorts the whole directory (decision 47), so the loaded rows are already in order.
  const sorted = items;
  const ids = useMemo(() => sorted.map((n) => n.id), [sorted]);
  const byID = useMemo(() => new Map(sorted.map((n) => [n.id, n])), [sorted]);
  const selected = useMemo(() => new Set(sel.ids), [sel.ids]);
  // cutSet is the items a cut has taken, dimmed in the listing (memoised, so the listing is not redrawn for nothing).
  const cutSet = useMemo(() => cutIDs(clip), [clip]);
  const hasSelection = sel.ids.length > 0;
  const picked = sel.ids.map((id) => byID.get(id)).filter((n): n is Node => n !== undefined);
  const rovingID = sel.focusID && byID.has(sel.focusID) ? sel.focusID : (ids[0] ?? null);
  const view = prefs.view;
  const folderRole = here ? (here.dir ? (here.dir.role ?? "viewer") : here.folderRole) : "viewer";
  // Files can go into a directory by its ID, so a shared directory whose folder root is not reachable takes them.
  // Folders are made, and folder uploads placed, by path from the root, so those need a reachable path.
  // While the next place is read, here is still the folder shown before: nothing is uploaded into it (fu3b).
  const canUploadHere = !busy && here !== null && (here.dir !== null || here.path !== null) && atLeast(folderRole, "contributor");
  const canUploadFolder = canUploadHere && here !== null && here.path !== null;
  const canMakeFolder = canUploadFolder;
  // The sidebar's New acts on the folder shown (decision 66). It is written when the place or what it takes changes, and cleared
  // when this page is left, so no other page sees a folder it did not show.
  const newKey = here ? [here.folder, here.dir?.id ?? "", here.path ?? "", canUploadHere, canUploadFolder].join("|") : "";
  useEffect(() => {
    newPlace.set(here ? placeOfHere(here, canUploadHere, canUploadFolder) : null);
    // The effect runs for a new place, or a change of what it takes: its key is the text of both.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [newKey]);
  useEffect(() => () => newPlace.set(null), []);
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
    // Each place on the way takes a drop, with the caller's role there: the same role a paste into it would need.
    if (here.rootReachable) out.push({ key: `root-${here.folder}`, label: rootLabel, href: filesURL(here.folder, null), drop: { folder: here.folder, dir: null, role: here.folderRole } });
    for (const n of here.trail) out.push({ key: n.id, label: n.name, href: filesURL(here.folder, n.id), drop: { folder: here.folder, dir: n.id, role: n.role } });
    if (here.dir) out.push({ key: here.dir.id, label: here.dir.name, href: filesURL(here.folder, here.dir.id), drop: { folder: here.folder, dir: here.dir.id, role: here.dir.role } });
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

  // lastTapOpen is when a finger last opened an item. A tap soon after it is the second tap of a double tap, and the rows
  // it would reach may be the folder's just opened (tap.ts), so it does not open anything.
  const lastTapOpen = useRef(-Infinity);
  function onRowClick(index: number, e: Click) {
    // A finger's tap opens the item, as a tap on a link does; a mouse's click selects it, and selects nothing else.
    if (e.touch) {
      const now = performance.now();
      if (!tapOpens(lastTapOpen.current, now)) return;
      const n = sorted[index];
      if (n) {
        lastTapOpen.current = now;
        openNode(n);
      }
      return;
    }
    setSelection(clickSelect(current(), ids, index, { shift: e.shiftKey, toggle: e.ctrlKey || e.metaKey }));
  }

  // selectEverything selects every row loaded. When more pages exist it says so: the rest are not selected until they are loaded.
  function selectEverything() {
    setSelection(selectAll(ids));
    if (hasMore) void message.info(t("files.selectAll.partial", { count: ids.length }));
  }

  function onSelectAll(checked: boolean) {
    if (checked) selectEverything();
    else setSelection(EMPTY);
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
      if (!changed() && !signingOut()) void message.error(fileErrorText(err, lang));
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
    if (viewable(n)) {
      openPreview(n);
    } else {
      setTarget(n);
      setDialog("details");
    }
  }

  function openPreview(n: Node) {
    playAsk.current++;
    const list = sorted.filter((x) => viewable(x));
    setPreviewItems(list.map(gated));
    setPreviewIndex(Math.max(0, list.findIndex((x) => x.id === n.id)));
    // A video or audio file opens the folder's playlist, from that file; other files keep the steps through the list.
    setPreviewMedia(isPlayable(n) ? { start: gated(n), folder: folderPager() } : null);
    setDialog("preview");
  }

  // gatedReader reads a folder's pages for a playlist. A later page shows the space's no-download rule as the first does, so
  // the player offers no Download the server would refuse.
  function gatedReader(folder: string, dir: string | null) {
    const read = folderPageReader(folder, dir, prefs.sort);
    return async (cursor: string) => {
      const page = await read(cursor);
      return { ...page, items: page.items.map(gated) };
    };
  }

  // folderPager reads the folder's playable files, a page at a time, from the listing as it has them: the loaded files in
  // the listing's order, then the pages the listing has not read yet.
  function folderPager(): Pager {
    const pages = childrenQ.data?.pages ?? [];
    const cursor = pages.length > 0 ? (pages[pages.length - 1].next_cursor ?? null) : null;
    const folder = folderID as string;
    const dir = dirID;
    // The cursor belongs to the listing's sort, so the pages are read with the same sort (lib/files/folderPages.ts).
    return new Pager(sorted.map(gated), cursor, gatedReader(folder, dir));
  }

  // playSelected plays the selected files that can play, in the listing's order, from the first of them.
  function playSelected() {
    playAsk.current++;
    const list = sorted.filter((x) => selected.has(x.id) && isPlayable(x)).map(gated);
    if (list.length === 0) return;
    setPreviewItems(list);
    setPreviewIndex(0);
    setPreviewMedia({ start: list[0], folder: folderPager(), selection: list });
    setDialog("preview");
  }

  // startMedia opens the player on a playlist that pages through a folder, from its first file.
  function startMedia(pager: Pager) {
    const list = pager.loaded;
    setPreviewItems(list);
    setPreviewIndex(0);
    setPreviewMedia({ start: list[0], folder: pager });
    setDialog("preview");
  }

  // playHere plays the audio and video files of the folder on screen, in the listing's order (decision 61).
  function playHere() {
    playAsk.current++;
    const pager = folderPager();
    if (pager.loaded.length > 0) startMedia(pager);
  }

  // playFolder plays the audio and video files directly inside a folder, in the listing's sort. Its pages are read until one
  // has such a file. A newer press, or a move to another place, drops the answer (playAsk).
  async function playFolder(dir: Node) {
    const my = ++playAsk.current;
    const folder = folderID as string;
    const fail = (text: string) => {
      if (my === playAsk.current && !signingOut()) void message.error(text);
    };
    try {
      const first = await childrenPage(folder, dir.id, undefined, prefs.sort);
      if (my !== playAsk.current) return;
      // A subfolder is in the same space as this place, so the same no-download rule shows on its files.
      const pager = new Pager(first.items.map(gated), first.next_cursor ?? null, gatedReader(folder, dir.id));
      while (pager.loaded.length === 0 && pager.hasMore) {
        const got = await pager.more();
        if (my !== playAsk.current) return;
        if (got.kind === "failed") {
          fail(t("error.generic"));
          return;
        }
      }
      if (pager.loaded.length === 0) {
        void message.info(t("files.playMedia.none"));
        return;
      }
      startMedia(pager);
    } catch (err) {
      fail(fileErrorText(err, lang));
    }
  }

  function focusRow(id: string | null) {
    if (!id) return;
    requestAnimationFrame(() => {
      const el = Array.from(listRef.current?.querySelectorAll<HTMLElement>("[data-id]") ?? []).find((x) => x.dataset.id === id);
      el?.focus();
    });
  }

  // tileRowDelta is how far the focus moves one row up (dir -1) or down (dir 1) in the tile view: the tile one row away on the
  // screen, from the tiles' places (rowStep), in the one flat order of the items. A tile of the order that is not on the
  // screen gives no step, so the focus stays put. There is no column count: the bands do not share one.
  function tileRowDelta(dir: 1 | -1): number {
    const root = listRef.current;
    const from = current().focus ?? -1;
    if (!root || from < 0) return 0;
    const els = new Map<string, HTMLElement>();
    root.querySelectorAll<HTMLElement>("[data-id]").forEach((el) => {
      if (el.dataset.id) els.set(el.dataset.id, el);
    });
    const places: (TilePlace | null)[] = ids.map((id) => {
      const el = els.get(id);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { top: r.top, x: r.x, width: r.width };
    });
    return rowStep(places, from, dir);
  }

  function onKey(e: ReactKeyboardEvent<HTMLElement>) {
    // While the folder left behind is busy, its rows take no key (fu4): the page's own notice, if any, is shown by onShortcut.
    if (renameRef.current || busy) return;
    const rtl = document.documentElement.dir === "rtl";
    // cols is 1: Up and Down in the tile view take their step from the tiles' places (tileRowDelta), not from a column count.
    const action = keyAction(e, { cols: 1, grid: view === "grid", rtl, count: ids.length, mac: MAC_KEYS });
    if (!action) return;
    switch (action.kind) {
      case "move": {
        e.preventDefault();
        // Up and Down in the tile view move to the tile one row away on the screen, across the folder band and the file band.
        const vertical = (e.key === "ArrowUp" || e.key === "ArrowDown") && view === "grid";
        const delta = vertical ? tileRowDelta(e.key === "ArrowDown" ? 1 : -1) : action.delta;
        const next = moveFocus(current(), ids, delta, action.extend);
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
      case "selectAll":
        e.preventDefault();
        selectEverything();
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

  // onShortcut takes the keys that act on the page as a whole: up a folder (Backspace, Alt+Up), copy, cut, paste, and
  // Escape to cancel a cut. It does nothing while a person types in a field, or while a dialog is open.
  function onShortcut(e: KeyboardEvent) {
    if (e.defaultPrevented || renameRef.current || isTextField(e.target) || dialogRef.current !== null || conflict !== null) return;
    if (dialogOpen()) return;
    if (!here) return;
    // While the rows on screen are the folder left behind (busy), no key acts on them (fu3b review 1). A key that would have
    // acted says why it did nothing, once (fu4, decision 80).
    if (busy) {
      noteBusyKey(e);
      return;
    }
    // The context-menu key and Shift+F10 open the menu of the focused row, as a right click does.
    if (e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey)) {
      e.preventDefault();
      openMenuFromFocus();
      return;
    }
    const rtl = document.documentElement.dir === "rtl";
    const action = keyAction(e, { cols: 1, grid: view === "grid", rtl, count: ids.length, mac: MAC_KEYS });
    switch (action?.kind) {
      case "up":
        e.preventDefault();
        goUp();
        return;
      // A key held down acts once. A text selected on the page is left to the browser's own copy.
      case "copy":
        if (picked.length === 0 || e.repeat || window.getSelection()?.toString()) return;
        e.preventDefault();
        copyPicked();
        return;
      case "cut":
        if (picked.length === 0 || e.repeat) return;
        e.preventDefault();
        cutPicked();
        return;
      case "paste":
        if (!clip || e.repeat) return;
        e.preventDefault();
        void pasteHere();
        return;
      case "escape":
        if (clip?.mode === "cut") clearClip();
        return;
    }
  }

  // noteBusyKey says, once per busy period, that the folder is still opening, for a key that would have acted on it (fu4).
  // It uses the app's own notice, as copying does, so assistive technology hears it the way the other notices are heard.
  function noteBusyKey(e: KeyboardEvent) {
    // A control keeps its own keys (a button, a link, a field): they are not the listing's (fu4 review 2).
    if (e.target instanceof Element && e.target.closest('button, a, input, textarea, select, [role="button"]')) return;
    const kind = keyAction(e, { cols: 1, grid: view === "grid", rtl: document.documentElement.dir === "rtl", count: ids.length, mac: MAC_KEYS })?.kind;
    if (!kind || !BUSY_NOTICE_KEYS.has(kind) || busyNoticed.current) return;
    // Only a key that would have acted says so: copy, cut, Delete and F2 need a picked item, paste needs something on the clipboard.
    const acts = kind === "paste" ? clip !== null : kind === "copy" || kind === "cut" || kind === "delete" || kind === "f2" ? picked.length > 0 : true;
    if (!acts) return;
    busyNoticed.current = true;
    setBusyNote(t("files.busy.keyWait"));
    void message.info(t("files.busy.keyWait"));
  }

  // openMenuFromFocus opens the menu of the focused row, or the place's menu when no row has the focus, from the keyboard. The
  // row is selected first, as a right click selects it. Focus goes into the menu, and back to the row when it closes unchosen.
  function openMenuFromFocus() {
    const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const row = focused?.closest<HTMLElement>("[data-id]") ?? null;
    const anchor = row ?? focused ?? sectionRef.current;
    if (!anchor) return;
    const box = anchor.getBoundingClientRect();
    const id = row?.dataset.id;
    if (id && !sel.ids.includes(id)) {
      const index = ids.indexOf(id);
      setSelection({ ids: [id], anchor: index, focus: index });
    }
    menuOrigin.current = anchor;
    menuFromKey.current = true;
    menuChosen.current = false;
    setMenu(menuAt(box.left + 24, box.top + Math.min(box.height, 48), row ? "selection" : "space"));
  }

  // copyPicked and cutPicked put the selection on the clipboard. A cut needs the right to move every item picked, and a
  // refused cut says so and takes nothing.
  function copyPicked() {
    setClip("copy", picked);
    void message.info(t("files.clip.copied"));
  }

  // copyLinkOf puts this app's own address of an item on the clipboard. It is the page a signed-in person opens (/n/{id}); no
  // share link is made, and the notice does not show the address, which carries the item's ID.
  async function copyLinkOf(n: Node) {
    const ok = await copyText(nodeAddress(n.id));
    if (ok) void message.success(t("files.copyLink.done"));
    else void message.error(t("files.copyLink.failed"));
  }

  function cutPicked() {
    if (!allEdit) {
      void message.warning(t("files.cut.refused"));
      return;
    }
    setClip("cut", picked);
    void message.info(t("files.clip.cut"));
  }

  // pasteHere puts the clipboard into this folder: a copy, or a move for a cut. A place the caller cannot write to refuses it.
  // An item already in this place is not moved to it again; a name taken here goes through the conflict flow, as a move does.
  async function pasteHere() {
    if (pasting.current) return;
    const pasted = clip;
    if (!pasted || !here) return;
    if (!canUploadHere) {
      void message.warning(t("files.paste.refused"));
      return;
    }
    const nodes = pasted.mode === "cut" ? pasted.nodes.filter((n) => !(n.folder_id === here.folder && (n.parent_id ?? null) === dirID)) : pasted.nodes;
    if (nodes.length === 0) {
      // Every item is in this folder already: the cut ends, and the message says why nothing moved.
      clearClip();
      void message.info(t("files.paste.same"));
      return;
    }
    // Items that are here already leave the clipboard; the others stay on it until they are moved.
    if (nodes.length !== pasted.nodes.length) setClip(pasted.mode, nodes);
    pasting.current = true;
    try {
      await runTransfer(pasted.mode === "cut" ? "move" : "copy", nodes, { folder: here.folder, dir: dirID }, true);
    } finally {
      pasting.current = false;
    }
  }

  // onRowContext opens the menu of an item: a right-click, or a finger held on it. An item not yet selected is selected
  // first, so the menu acts on what it shows; a selected one keeps the whole selection.
  function onRowContext(id: string, index: number, x: number, y: number, keyboardOrigin?: HTMLElement) {
    if (!sel.ids.includes(id)) setSelection({ ids: [id], anchor: index, focus: index });
    if (keyboardOrigin) {
      menuOrigin.current = keyboardOrigin;
      menuFromKey.current = true;
      menuChosen.current = false;
    }
    setMenu(menuAt(x, y, "selection"));
  }

  // onSpaceContext opens the menu of the empty space in the browser. A row, a field, a dialog or the toolbar keeps the
  // browser's own menu or its own; the row's menu has already taken its right-click.
  function onSpaceContext(e: ReactMouseEvent<HTMLElement>) {
    if (e.defaultPrevented) return;
    const target = e.target;
    if (!(target instanceof HTMLElement) || target.closest('[role="toolbar"], nav, input, textarea, select, [contenteditable], .ant-modal, .ant-dropdown, .ant-drawer, [role="dialog"]')) return;
    // A place with nothing to offer keeps the browser's own menu: the menu is not opened, and nothing is prevented.
    if (!spaceActs.some((a) => !a.hidden)) return;
    e.preventDefault();
    setMenu(menuAt(e.clientX, e.clientY, "space"));
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
      // The panel's Delete button goes with the selection, so the focus is handed to the listing (see the effect on the selection).
      toolbarFocus.current = true;
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
    if (failed > 0) void message.error(t("files.err.someFailed", { count: failed }));
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

  // issueNotice names the items a paste could not change: up to five, then how many more.
  function issueNotice(issues: Array<{ name: string; reason: string }>) {
    const shown = issues.slice(0, 5);
    const more = issues.length - shown.length;
    return (
      <span>
        {issues.length > 1 && <span style={{ display: "block" }}>{t("files.err.someFailed", { count: issues.length })}</span>}
        {shown.map((i, k) => (
          <span key={k} style={{ display: "block" }}>{`${i.name}: ${i.reason}`}</span>
        ))}
        {more > 0 && <span style={{ display: "block" }}>{t("files.err.andMore", { count: more })}</span>}
      </span>
    );
  }

  // runTransfer moves or copies the picked nodes into dest. fromClip says they came off the clipboard: each is read again first,
  // so a move carries the etag the server has now, and one that no longer exists is not sent and is named. A cut that moved, or
  // that no longer exists, leaves the clipboard; the ones that failed stay cut for another try.
  // reread (on for a paste, and for a drag) reads each item again first; fromClip says the items came off the clipboard, so the
  // clipboard is trimmed to match. A drag reads again and leaves the clipboard alone.
  async function runTransfer(mode: "move" | "copy", picked: Node[], dest: Destination, fromClip = false, reread = fromClip) {
    setDialog(null);
    const changed = sessionChanged();
    const moved: Array<{ node: Node; from: { folder: string; parent?: string; name: string } }> = [];
    const issues: Array<{ name: string; reason: string }> = [];
    const gone = new Set<string>();
    let batch: ConflictChoice | null = null;
    let jobsStarted = 0;
    const base: Transfer = dest.dir ? { folder: dest.folder, parent: dest.dir } : { folder: dest.folder, path: "/" };
    const nodes: Node[] = [];
    for (const n of picked) {
      if (!reread) {
        nodes.push(n);
        continue;
      }
      let cur: Node | null;
      try {
        cur = await statOrNull(n.id);
      } catch (err) {
        if (changed() || !mounted.current) return;
        issues.push({ name: n.name, reason: fileErrorText(err, lang) });
        continue;
      }
      if (changed() || !mounted.current) return;
      if (cur === null) {
        gone.add(n.id);
        issues.push({ name: n.name, reason: t("files.err.notFound") });
        continue;
      }
      // Someone else may have moved it into place since the drag or the cut: it is not sent, and no conflict is asked.
      if (mode === "move" && alreadyIn(cur, dest)) {
        gone.add(cur.id);
        continue;
      }
      nodes.push(cur);
    }
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
              watchJob(res.id, [{ folder: dest.folder, dir: dest.dir }]);
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
          issues.push({ name: n.name, reason: fileErrorText(err, lang) });
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
        await reload(moved.flatMap((m) => [{ folder: m.from.folder, dir: m.from.parent ?? null }, { folder: m.node.folder_id, dir: m.node.parent_id }]));
        return bad;
      });
      showUndo(label);
    }
    const current = get(clipboard);
    if (fromClip && current) {
      const drop = new Set(gone);
      if (current.mode === "cut") for (const m of moved) drop.add(m.node.id);
      const rest = current.nodes.filter((c) => !drop.has(c.id));
      if (rest.length === 0) clearClip();
      else if (rest.length !== current.nodes.length) setClip(current.mode, rest);
    }
    if (issues.length > 0) void message.error(issueNotice(issues));
    else if (mode === "copy" && jobsStarted === 0) void message.success(t("files.copy.done"));
    await reload([{ folder: dest.folder, dir: dest.dir }, ...nodes.map((n) => ({ folder: n.folder_id, dir: n.parent_id }))]);
  }

  // watchJob polls a copy that the server runs as a job, until it finishes.
  function watchJob(id: string, also: Branch[] = []) {
    setJobs((prev) => [...prev, { id, status: "queued" }]);
    const poll = async () => {
      if (!mounted.current) return;
      try {
        const j = await getJob(id);
        if (!mounted.current) return;
        setJobs((prev) => prev.map((x) => (x.id === id ? { id, status: j.status } : x)));
        if (j.status === "queued" || j.status === "running") setTimeout(() => void poll(), 1500);
        else if (j.status === "succeeded") await reload(also);
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

  // The dialog is the one the sidebar's New opens (NewFolderDialog, decision 77 item fu3b). Here it reads the listing again once
  // the folder is made, as the page's own dialog did.
  function openNewFolder() {
    setDialog("newFolder");
  }

  // ---- uploads ----

  function queueFiles(files: DroppedFile[]) {
    if (!here || busy) return;
    const skipped = queueInto(placeOfHere(here, canUploadHere, canUploadFolder).where, files);
    if (skipped > 0) void message.warning(t("files.upload.noPath"));
  }

  // openFilePicker is the one path to the page's file picker: the New menu's Upload files and the empty folder's button both call it,
  // so both open the same input, whose change goes to onFilesPicked and then queueFiles (decision 85).
  function openFilePicker() {
    fileInputRef.current?.click();
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
    if (!ok) void message.warning(t("files.err.downloadRestricted"));
  }

  // makeZipOf makes a zip of the items selected and opens the dialog that shows it. The download starts by itself when
  // the zip is ready, as a browser link.
  function makeZipOf(items: Node[]) {
    zipStop.current?.();
    setZip({ kind: "making" });
    setDialog("zip");
    zipStop.current = makeZip(
      items.map((n) => n.id),
      {
        lang,
        onReady: (skipped, zipID) => setZip({ kind: "ready", skipped, zip: zipID }),
        onFailed: (text) => setZip({ kind: "failed", text }),
        onGiveUp: () => setZip({ kind: "slow" }),
      },
    );
  }

  // zipOffered is the zip action for a selection: a folder, or several items. A shortcut is never zipped, and an item the
  // no-download rule keeps from the caller is not offered (the server refuses it again).
  function zipOffered(items: Node[]): boolean {
    if (items.length === 0 || (items.length === 1 && items[0].kind !== "dir")) return false;
    return items.every((n) => n.kind !== "shortcut" && downloadAllowed(gated(n)));
  }

  function changePrefs(next: ViewPrefs) {
    setPrefs(next);
    writePrefs(next);
  }

  function sortBy(key: SortKey) {
    changePrefs({ ...prefs, sort: nextSort(prefs.sort, key) });
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
  // My files is the person's own folder, and its subfolders: the storage notice shows there only.
  const ownFolder = personalID !== null && here.folder === personalID;
  const selectedNodes = picked;
  const rowLabel = titleName;

  // barActs is the one list of actions: the toolbar shows it, the right-click menu of a row or a selection is built from it,
  // and at phone width the one-row bar takes its main action and a More menu from it. Each action is hidden when the caller
  // may never perform it in this listing (the listing is read for that), and disabled when the selection does not allow it
  // now. So the three places cannot disagree about permissions.
  const editable = items.some((n) => can.edit(n.role));
  const downloadable = items.some((n) => n.kind === "file" && downloadAllowed(gated(n)));
  const zippable = items.some((n) => n.kind !== "shortcut" && downloadAllowed(gated(n)));
  const versionable = items.some((n) => n.kind === "file" && atLeast(n.role, "viewer"));
  const shortcutable = spaces.length > 0 && items.some((n) => n.kind !== "shortcut");
  const shareable = items.some((n) => canShareItem(n));
  const ic = { size: 16, "aria-hidden": true } as const;
  // Each action's fits says whether the selection is of a kind it applies to. The toolbar does not change with the selection
  // (an action that does not fit is disabled there); the menus leave such an action out.
  // buildActs is the one list, for a set of items: the selection for the toolbar and the menus, and the dragged set for a drag,
  // which asks the same Move and Copy whether they are enabled for those items (see gestureCtl).
  const buildActs = (selectedNodes: Node[]): BarAct[] => {
    const single = selectedNodes.length === 1 ? selectedNodes[0] : null;
    const pickedFile = selectedNodes.length === 0 || selectedNodes.some((n) => n.kind === "file");
    const allEdit = selectedNodes.length > 0 && selectedNodes.every((n) => can.edit(n.role));
    return [
    {
      key: "open",
      label: t("files.act.open"),
      icon: <FolderOpen {...ic} />,
      group: 1,
      section: "first",
      // Enter opens a folder, and a file with no preview (its details): the entry is there for either, so the key has a menu entry
      // (review fix 12).
      hidden: !items.some((n) => n.kind === "dir") && !(single !== null && !viewable(single)),
      fits: single !== null && (single.kind === "dir" || !viewable(single)),
      enabled: single !== null,
      run: () => {
        if (single) openNode(single);
      },
    },
    {
      key: "preview",
      label: single ? t(previewKey(single)) : t("files.act.preview"),
      icon: single && previewKey(single) === "files.act.play" ? <Play {...ic} /> : <Eye {...ic} />,
      group: 1,
      section: "first",
      hidden: !items.some(viewable),
      fits: selectedNodes.length === 0 || selectedNodes.some(viewable),
      enabled: single !== null && viewable(single),
      run: () => {
        if (single) openPreview(single);
      },
    },
    {
      key: "openEditor",
      // Open in editor for a file the caller may edit, Open in viewer for one it may only read (decision 72).
      label: single && editorEntry(single, editorOn) === "view" ? t("files.act.openViewer") : t("files.act.openEditor"),
      icon: single && editorEntry(single, editorOn) === "view" ? <BookOpen {...ic} /> : <Pencil {...ic} />,
      group: 1,
      section: "first",
      hidden: !items.some((n) => editorEntry(n, editorOn) !== null),
      fits: single !== null && editorEntry(single, editorOn) !== null,
      enabled: single !== null && editorEntry(single, editorOn) !== null,
      run: () => {
        if (single) {
          const mode = editorEntry(single, editorOn);
          if (mode) go(editorPath(single.id, mode, path + search));
        }
      },
    },
    {
      key: "playSelected",
      label: t("files.act.playSelected"),
      icon: <Play {...ic} />,
      group: 1,
      section: "first",
      hidden: !items.some(isPlayable),
      // Play selected plays the playable files of a selection of two or more, as a playlist.
      fits: selectedNodes.length > 1 && selectedNodes.some(isPlayable),
      enabled: true,
      run: () => playSelected(),
    },
    {
      key: "playMedia",
      label: t("files.act.playMedia"),
      icon: <Play {...ic} />,
      group: 1,
      section: "first",
      hidden: !items.some((n) => n.kind === "dir"),
      // A folder's own audio and video files. A file has Play, and a selection of files has Play selected.
      fits: single?.kind === "dir",
      enabled: true,
      run: () => {
        if (single) void playFolder(single);
      },
    },
    {
      key: "download",
      label: t("files.act.download"),
      icon: <Download {...ic} />,
      group: 1,
      section: "first",
      hidden: !downloadable,
      fits: pickedFile,
      enabled: single !== null && single.kind === "file" && downloadAllowed(gated(single)),
      run: () => {
        if (single) void download(single);
      },
    },
    {
      key: "zip",
      label: t("files.act.zip"),
      icon: <FileArchive {...ic} />,
      group: 1,
      section: "first",
      hidden: !zippable,
      // A zip is for folders and selections: one file is downloaded, so no zip is offered for it.
      fits: single?.kind !== "file",
      enabled: zipOffered(selectedNodes),
      run: () => makeZipOf(selectedNodes),
    },
    {
      key: "rename",
      label: t("files.act.rename"),
      icon: <Pencil {...ic} />,
      group: 3,
      section: "first",
      hidden: !editable,
      fits: true,
      enabled: single !== null && can.edit(single.role),
      run: () => {
        if (single) startRename(single);
      },
    },
    {
      key: "share",
      label: t("share.open"),
      // In the menu the entry opens the share dialog, as the Share submenu's title does not: its ellipsis says so.
      menuLabel: `${t("share.open")}…`,
      icon: <Share2 {...ic} />,
      group: 2,
      section: "share",
      // Hidden from a caller who may share none of the listed items, as a role that can never share sees no share action. For
      // one item it is disabled unless that item may be shared (the share dialog limits what each role sees of it).
      hidden: !shareable,
      fits: true,
      enabled: single !== null && canShareItem(single),
      run: () => {
        if (!single) return;
        setTarget(single);
        setDialog("share");
      },
    },
    {
      key: "copyLink",
      label: t("files.act.copyLink"),
      icon: <LinkIcon {...ic} />,
      group: 2,
      section: "share",
      // This app's own address of the item: it makes no share link and grants nothing, so it is there for anyone who sees the item.
      hidden: false,
      fits: true,
      enabled: single !== null,
      run: () => {
        if (single) void copyLinkOf(single);
      },
    },
    {
      key: "details",
      label: t("files.act.details"),
      icon: <Info {...ic} />,
      group: 2,
      section: "info",
      hidden: false,
      fits: true,
      enabled: single !== null,
      run: () => {
        if (!single) return;
        setTarget(single);
        setDialog("details");
      },
    },
    {
      key: "versions",
      label: t("files.act.versions"),
      icon: <History {...ic} />,
      group: 2,
      section: "info",
      hidden: !versionable,
      fits: pickedFile,
      enabled: single !== null && single.kind === "file" && atLeast(single.role, "viewer"),
      run: () => {
        if (!single) return;
        setTarget(single);
        setDialog("versions");
      },
    },
    {
      key: "move",
      label: t("files.act.move"),
      icon: <FolderInput {...ic} />,
      group: 4,
      section: "organize",
      hidden: !editable,
      fits: true,
      enabled: allEdit,
      run: () => startPick("move", selectedNodes),
    },
    {
      key: "copy",
      label: t("files.act.copy"),
      icon: <CopyPlus {...ic} />,
      group: 4,
      section: "organize",
      hidden: false,
      fits: true,
      enabled: selectedNodes.length > 0,
      run: () => startPick("copy", selectedNodes),
    },
    {
      key: "shortcut",
      label: t("files.act.shortcut"),
      icon: <Link2 {...ic} />,
      group: 3,
      section: "organize",
      hidden: !shortcutable,
      fits: selectedNodes.length === 0 || selectedNodes.some((n) => n.kind !== "shortcut"),
      enabled: single !== null && single.kind !== "shortcut",
      run: () => {
        if (!single) return;
        setTarget(single);
        setDialog("pickShortcut");
      },
    },
    { key: "cut", label: t("files.act.cut"), icon: <Scissors {...ic} />, group: 4, section: "organize", hidden: !editable, fits: true, enabled: allEdit, run: cutPicked },
    { key: "copyClip", label: t("files.act.copyClip"), icon: <Copy {...ic} />, group: 4, section: "organize", hidden: false, fits: true, enabled: selectedNodes.length > 0, run: copyPicked },
    { key: "paste", label: t("files.act.paste"), icon: <ClipboardPaste {...ic} />, group: 4, section: "organize", hidden: !canUploadHere, fits: true, enabled: clip !== null, run: () => void pasteHere() },
    // Star is a toggle: the hover icon, the menu entry and the More menu share this one. Unstarred items show no star at all.
    {
      key: "star",
      label: single?.starred ? t("star.removeTip") : t("star.addTip"),
      icon: <Star {...ic} fill={single?.starred ? "currentColor" : "none"} />,
      group: 4,
      section: "organize",
      hidden: !items.some((n) => n.starred !== undefined),
      fits: true,
      enabled: single !== null && single.starred !== undefined,
      run: () => {
        if (single) void toggleStar(single);
      },
    },
    // Delete is marked as destructive in the toolbar and in the menus alike; the menu says Move to trash, which is what it does.
    { key: "delete", label: t("files.act.delete"), menuLabel: t("files.act.trash"), icon: <Trash2 {...ic} />, group: 5, section: "trash", hidden: !editable, fits: true, enabled: allEdit, danger: true, run: () => askDelete(selectedNodes) },
    ];
  };
  const acts = buildActs(selectedNodes);
  // The rights decide what is hidden, so the toolbar keeps its shape as the selection changes.
  const barActs = acts.filter((a) => !a.hidden);
  // The panel keeps its icons in order; the rest of the selection actions are in its More menu.
  // Copy link has an icon where the room allows (all widths with a mouse; from 480 px on a finger); below that it is in More.
  const panelKeys = roomy ? (coarse && !spacious ? ["share", "download", "move", "delete"] : ["share", "copyLink", "download", "move", "delete"]) : ["share", "download"];
  const panelIcons = panelKeys.map((k) => barActs.find((a) => a.key === k)).filter((a): a is BarAct => a !== undefined);
  const panelMore = barActs.filter((a) => !panelKeys.includes(a.key));
  // newActs is the New menu: a folder, and the files or a folder uploaded. spaceActs is the menu of the empty space: the same,
  // and a paste. Both follow the toolbar's rules.
  // The three blank documents (decision 91): shown only while office editing is on and the folder takes new files.
  const docKinds = canUploadHere ? documentKinds(shownPlace, editorOn) : [];
  const docActs: BarAct[] = [
    { key: "doc-docx", label: t("files.act.document"), icon: <FileText {...ic} />, group: 1, section: "first", hidden: !docKinds.includes("docx"), fits: true, enabled: true, run: () => makeDocument("docx") },
    { key: "doc-xlsx", label: t("files.act.spreadsheet"), icon: <FileSpreadsheet {...ic} />, group: 1, section: "first", hidden: !docKinds.includes("xlsx"), fits: true, enabled: true, run: () => makeDocument("xlsx") },
    { key: "doc-pptx", label: t("files.act.presentation"), icon: <Presentation {...ic} />, group: 1, section: "first", hidden: !docKinds.includes("pptx"), fits: true, enabled: true, run: () => makeDocument("pptx") },
  ];
  const newActs: BarAct[] = [
    { key: "newFolder", label: t("files.act.newFolder"), icon: <FolderPlus {...ic} />, group: 1, section: "first", hidden: !canMakeFolder, fits: true, enabled: true, run: openNewFolder },
    { key: "upload", label: t("files.act.upload"), icon: <Upload {...ic} />, group: 1, section: "first", hidden: !canUploadHere, fits: true, enabled: true, run: openFilePicker },
    { key: "uploadFolder", label: t("files.act.uploadFolder"), icon: <FolderUp {...ic} />, group: 1, section: "first", hidden: !canUploadFolder, fits: true, enabled: true, run: () => folderInputRef.current?.click() },
    ...docActs,
  ];
  const hasMedia = items.some(isPlayable);
  const spaceActs: BarAct[] = [
    { key: "playMedia", label: t("files.act.playMedia"), icon: <Play {...ic} />, group: 0, section: "first", hidden: !hasMedia, fits: true, enabled: true, run: () => playHere() },
    ...newActs,
    { key: "paste", label: t("files.act.paste"), icon: <ClipboardPaste {...ic} />, group: 2, section: "first", hidden: !canUploadHere, fits: true, enabled: clip !== null, run: () => void pasteHere() },
  ];
  // menuItems turns actions into the items of a menu: a divider between the groups, the hidden and the non-fitting actions left
  // out, and a disabled one shown greyed. Choosing an item closes the menu before the action runs.
  const menuItems = (list: BarAct[]) => {
    const shown = list.filter((a) => !a.hidden && a.fits);
    return shown.flatMap((a, i) => {
      const item = {
        key: a.key,
        label: a.label,
        icon: a.icon,
        danger: a.danger,
        disabled: !a.enabled,
        // A finger needs 44 pixels: the antd menu's own height does not reach a dropdown's items, so the height is set here.
        style: touchItem,
        onClick: () => {
          menuChosen.current = true;
          setMenu(null);
          a.run();
        },
      };
      return i > 0 && shown[i - 1].group !== a.group ? [{ type: "divider" as const, key: `${a.key}-divider` }, item] : [item];
    });
  };
  // actItem is one entry of a menu built from an action: its shortcut at the right, and the same call as the toolbar's.
  const actItem = (a: BarAct) => ({
    key: a.key,
    label: a.menuLabel ?? a.label,
    icon: a.icon,
    danger: a.danger,
    disabled: !a.enabled,
    // The shortcut is drawn at the entry's right from its data-hint (index.css), so the entry's text is its name alone.
    // The hint is hidden from a screen reader (review fix 9): the entry's name is its text alone, and the shortcut is named by
    // aria-keyshortcuts.
    extra: shortcutHint(a.key, MAC_KEYS) ? <span aria-hidden="true" className="menu-hint" data-hint={shortcutHint(a.key, MAC_KEYS)} /> : undefined,
    "aria-keyshortcuts": shortcutKeys(a.key, MAC_KEYS),
    // A finger needs 44 pixels, as on each other item.
    style: touchItem,
    onClick: () => {
      menuChosen.current = true;
      setMenu(null);
      a.run();
    },
  });
  // groupedItems is the menu of a file, a folder or a selection, grouped as Drive groups it (decision 70): the first group flat,
  // then Share, Organize and Information as submenus, then Move to trash. A submenu with nothing to show is not there.
  const GROUP_TITLE = { share: "share.open", organize: "files.menu.organize", info: "files.menu.info" } as const;
  const groupIcon = { share: <Share2 {...ic} />, organize: <FolderInput {...ic} />, info: <Info {...ic} /> } as const;
  const groupedItems = (list: BarAct[]) =>
    groupMenu(list).map((piece, i) => {
      if (piece.kind === "divider") return { type: "divider" as const, key: `divider-${i}` };
      if (piece.kind === "item") return actItem(piece.act);
      return {
        key: `group-${piece.section}`,
        label: t(GROUP_TITLE[piece.section]),
        icon: groupIcon[piece.section],
        style: touchItem,
        // A submenu has the root menu's height limit and scroll, so a short window reaches its last entry (review fix 5).
        popupStyle: MENU_SCROLL,
        children: piece.acts.map(actItem),
      };
    });
  // rowActsFor gives the icons at a list row's end for one item: Share, Download (or Download as zip for a folder), Rename and
  // Star. Each is the same action the menu runs, built for that item alone, so the permissions are the menu's.
  const rowActsFor = (n: Node): RowAct[] => {
    const list = buildActs([n]);
    const pick = (key: string) => list.find((a) => a.key === key && !a.hidden && a.fits);
    const download = pick("download") ?? pick("zip");
    return [pick("share"), download, pick("rename"), pick("star")].flatMap((a) =>
      a ? [{ key: a.key, label: a.label, icon: a.icon, enabled: a.enabled, run: a.run }] : [],
    );
  };
  // On a touch screen the toolbar buttons and the menus' items are a finger's size, 44 pixels.
  const menuTheme = coarse ? { components: { Menu: { itemHeight: 44 } } } : {};
  // A toolbar button is a 44 pixel target on a finger, icon-only ones included (their width is set too).
  const touchSize = coarse ? { minHeight: 44, minWidth: 44 } : undefined;
  // A menu item is 44 pixels tall: its padding is taken out of the height, so the item is not taller than the target.
  const touchItem = coarse ? { height: 44, paddingBlock: 0, lineHeight: "44px" } : undefined;
  // Clear disables itself once the selection is empty, so the focus moves to the listing first: a disabled button drops it.
  const clearPicked = () => {
    listRef.current?.querySelector<HTMLElement>("[data-id][tabindex='0']")?.focus();
    setSelection(EMPTY);
  };
  // The sort menu lists the fields, then the direction; the current field and direction are marked. Choosing a field keeps
  // the direction, so the menu sets the order it shows rather than toggling it as a column click does.
  const sortMenu = {
    selectable: true,
    selectedKeys: [`sort:${prefs.sort.key}`, `dir:${prefs.sort.dir}`],
    items: [
      { key: "sort:name", label: t("files.sort.name"), style: touchItem },
      { key: "sort:modified", label: t("files.sort.modified"), style: touchItem },
      { key: "sort:size", label: t("files.sort.size"), style: touchItem },
      { type: "divider" as const, key: "sort-divider" },
      { key: "dir:asc", label: t("files.sort.asc"), style: touchItem },
      { key: "dir:desc", label: t("files.sort.desc"), style: touchItem },
    ],
    onClick: ({ key }: { key: string }) => {
      if (key.startsWith("sort:")) changePrefs({ ...prefs, sort: { key: key.slice(5) as SortKey, dir: prefs.sort.dir } });
      else changePrefs({ ...prefs, sort: { key: prefs.sort.key, dir: key.slice(4) as SortDir } });
    },
  };
  // The view toggle shows the view it switches to.
  const switchTo = view === "list" ? { to: "grid" as const, label: t("files.view.grid") } : { to: "list" as const, label: t("files.view.list") };
  // The toolbar is one row that stays on screen above the listing: the shell's header is 56 pixels, and the rows scroll under it.
  // The selection panel takes the toolbar's row while something is selected: one full-width bar at the same height, above the
  // listing (on every width, as the owner asked). X clears, then the count, then icons with tooltips; the rest are in More.
  const countText = translatePlural(lang, "files.selected", sel.ids.length);
  // The bar is a pill with its icons (decision 68): clear, the count, the actions, a spacer and More.
  const selectionPanel = (
    <SelectionBar
      label={t("files.selection.label")}
      countText={countText}
      clearLabel={t("files.act.clear")}
      onClear={clearPicked}
      coarse={coarse}
      actions={panelIcons.map((a) => ({ key: a.key, label: a.label, icon: a.icon, enabled: a.enabled, run: a.run }))}
      moreMenu={
        panelMore.length > 0 ? (
          <ConfigProvider theme={menuTheme}>
            <Dropdown trigger={["click"]} autoAdjustOverflow={MENU_FIT} overlayStyle={MENU_POPUP} menu={{ items: groupedItems(panelMore), style: MENU_SCROLL, triggerSubMenuAction: coarse ? "click" : "hover" }}>
              <IconButton label={t("files.act.more")} icon={<EllipsisVertical {...ic} />} aria-haspopup="menu" />
            </Dropdown>
          </ConfigProvider>
        ) : null
      }
    />
  );
  // The row is 48 px with a mouse (the panel's 40 and 4 above and below) and 52 on a finger, where the panel fills it.
  const stickyBar = { position: "sticky" as const, top: HEADER_H, zIndex: 5, background: token.colorBgLayout, display: "flex", flexWrap: "nowrap" as const, alignItems: "center", gap: 8, boxSizing: "border-box" as const, minHeight: toolbarH(coarse), minWidth: 0, paddingBlock: rowPad(coarse) };

  // gestureCtl answers the pointer gestures from the page as it is now. A drag asks the same action list as the toolbar: Move and
  // Copy, built for the dragged items, must be enabled for the drop to be allowed; the place is judged by dropDecision, with the
  // role a paste needs. A drop then runs the same transfer as a paste does, which reads each item again for its current etag,
  // asks about a taken name, and pushes its undo.
  const WHY_KEY = { role: "files.drag.no.role", self: "files.drag.no.self", here: "files.drag.no.here", move: "files.drag.no.move" } as const;
  gestureCtl.current = {
    enabled: () => here !== null && !busy && renameRef.current === null && dialogRef.current === null && conflict === null && !dialogOpen(),
    startDrag: (id) => {
      const n = byID.get(id);
      if (!n) return null;
      if (sel.ids.includes(id) && picked.length > 0) return picked;
      const index = ids.indexOf(id);
      setSelection({ ids: [id], anchor: index, focus: index });
      return [n];
    },
    decide: (nodes, dest: Dest, copy) => {
      const list = buildActs(nodes);
      const allowed = (key: string) => list.some((a) => a.key === key && !a.hidden && a.enabled);
      return dropDecision(nodes, dest, copy, { move: allowed("move"), copy: allowed("copy") });
    },
    drop: (decision, dest) => {
      void runTransfer(decision.mode, decision.nodes, { folder: dest.folder, dir: dest.dir }, false, true);
    },
    refuse: (why) => void message.warning(t(WHY_KEY[why])),
    selectedIDs: () => sel.ids,
    setBand: (hit) => {
      const first = hit.length > 0 ? ids.indexOf(hit[0]) : null;
      const last = hit.length > 0 ? ids.indexOf(hit[hit.length - 1]) : null;
      setSelection({ ids: hit, anchor: first, focus: last });
    },
    clear: () => setSelection(EMPTY),
  };
  const dragView = gestures.drag;
  const dragWhy = dragView?.why ?? null;
  const dragLine = dragView === null ? "" : dragView.name || t("files.drag.items", { count: dragView.count });
  const dragHint =
    dragView === null
      ? null
      : dragWhy
        ? t(WHY_KEY[dragWhy])
        : dragView.target !== null
          ? null
          : dragView.copy
            ? t("files.drag.hintCopy")
            : t("files.drag.hintMove");

  return (
    <div className="files-layout" style={{ display: "flex", alignItems: "flex-start", gap: 16, minWidth: 0 }}>
    <section
      ref={sectionRef}
      className="browser"
      aria-labelledby="browser-title"
      onDragEnter={onDragEnter}
      onDragLeave={onDragLeave}
      onDragOver={onDragOver}
      onDrop={onDrop}
      onContextMenu={onSpaceContext}
      // The rows keep their own height: a short folder must not stretch the header and the toolbar apart. The room under the
      // rows is the upload tray's, so the last rows can be scrolled clear of it. The section takes the room there is (see
      // ".files-layout" in index.css) and runs over the page's own padding to the window's bottom, so a rubber band, a right-click
      // or a dropped file works on all the empty space below the rows.
      style={{ position: "relative", display: "grid", gridTemplateColumns: "minmax(0, 1fr)", gap: 16, flex: "1 1 0", alignSelf: "stretch", minWidth: 0, marginBlockEnd: "calc(-1 * var(--sc-page-pad, 28px))", alignContent: "start", paddingBlockEnd: room }}
    >
      <header style={{ minWidth: 0, display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ flex: "1 1 auto", minWidth: 0 }}>
          <Breadcrumbs crumbs={crumbs} />
        </div>
        <h1 id="browser-title" className="sr-only">
          {titleName}
        </h1>
      </header>

      {childrenQ.isPending && stale === null ? (
        <div role="status" aria-busy="true">
          <span className="sr-only">{t("files.loading")}</span>
          <Skeleton active paragraph={{ rows: 6 }} />
        </div>
      ) : childrenQ.isError && childrenQ.data === undefined && kept === null ? (
        <div role="alert">
          <Alert type="error" showIcon message={fileErrorText(childrenQ.error, lang)} />
          <Button onClick={() => void childrenQ.refetch()} style={{ marginTop: 12 }}>
            {t("files.retry")}
          </Button>
        </div>
      ) : (
        <>
          {/* The change of folder is announced while the rows of the last read are kept (fu3b review 2). */}
          {busy && (
            <span role="status" className="sr-only">
              {t("files.loading")}
            </span>
          )}
          <span aria-live="polite" className="sr-only">{busyNote}</span>
          {notice && <Alert type="info" showIcon role="status" message={notice} closable onClose={() => setNotice(null)} />}
          {readFailure && <Alert type="info" showIcon role="status" message={readFailure} />}

          {/* One row, always on top: the New menu, the selection's actions (at desktop width), the count, Clear, and the views at the end.
              It stays on screen as the listing scrolls, so Delete is in reach of a row at the bottom of a long folder. */}
          <div
            role="toolbar"
            aria-label={t("files.toolbar")}
            data-testid="browser-toolbar"
            // The toolbar acts on the place shown, which is the previous one while the next is looked up: it is inert then (fu3b).
            {...(busy ? { inert: "" } : {})}
            style={stickyBar}
            onFocus={() => {
              toolbarFocus.current = true;
            }}
            onBlur={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as globalThis.Node | null)) toolbarFocus.current = false;
            }}
          >
            {/* The pickers stay mounted while a selection shows the panel: Upload and New work from either. */}
            {canUploadHere && (
              <>
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
            {hasSelection ? selectionPanel : (
              <>
            {(canMakeFolder || canUploadHere) && !docked && (
              <ConfigProvider theme={menuTheme}>
                {/* Under 480 px New is the plus icon, with its name as label and tooltip; a wider row keeps the word (decision 68). */}
                {spacious ? (
                  <Dropdown trigger={["click"]} autoAdjustOverflow={MENU_FIT} overlayStyle={MENU_POPUP} menu={{ items: menuItems(newActs), style: MENU_SCROLL }}>
                    <Button icon={<Plus {...ic} />} aria-haspopup="menu" style={touchSize}>
                      {t("files.act.new")}
                    </Button>
                  </Dropdown>
                ) : (
                  <Dropdown trigger={["click"]} autoAdjustOverflow={MENU_FIT} overlayStyle={MENU_POPUP} menu={{ items: menuItems(newActs), style: MENU_SCROLL }}>
                    <IconButton label={t("files.act.new")} icon={<Plus {...ic} />} aria-haspopup="menu" />
                  </Dropdown>
                )}
              </ConfigProvider>
            )}
            {/* Play media is the play icon at every width; its name is its label and tooltip (decision 68). */}
            {hasMedia && <IconButton label={t("files.act.playMedia")} icon={<Play {...ic} />} onClick={playHere} />}
            <span style={{ flex: "1 1 auto" }} />
            {view === "grid" && (
              <ConfigProvider theme={menuTheme}>
                <Dropdown trigger={["click"]} menu={sortMenu}>
                  <IconButton label={t("files.sort.label")} icon={<ArrowUpDown {...ic} />} />
                </Dropdown>
              </ConfigProvider>
            )}
            <IconButton
              data-testid="view-toggle"
              data-view={switchTo.to}
              label={switchTo.label}
              icon={switchTo.to === "grid" ? <LayoutGrid {...ic} /> : <List {...ic} />}
              onClick={() => changePrefs({ ...prefs, view: switchTo.to })}
            />
              </>
            )}
          </div>

          {/* The storage notice is in the person's own folder only, below the toolbar, so it never moves the toolbar. */}
          {ownFolder && <StorageNotice />}

          <div>
            {sorted.length === 0 ? (
              // The box needs canUploadHere, which is false while the place is busy (decision 77): the text shows as before then.
              canUploadHere ? (
                // Decision 85: the box says how to add files, and its one button is the New menu's Upload files (openFilePicker).
                // A drop on the box is a drop on the section, so it queues the files the same way (queueFiles).
                <div
                  data-testid="empty-folder"
                  style={{ padding: "32px 16px", textAlign: "center", color: token.colorTextSecondary, border: `1px dashed ${token.colorBorder}`, borderRadius: token.borderRadiusLG, display: "flex", flexDirection: "column", alignItems: "center", gap: 8 }}
                >
                  <Upload size={32} aria-hidden="true" color={token.colorTextTertiary} />
                  <h2 style={{ margin: 0, fontSize: token.fontSizeLG, fontWeight: 600, color: token.colorText }}>{t("files.empty")}</h2>
                  <p style={{ margin: 0 }}>{coarse ? t("files.emptyTouch") : t("files.emptyMouse")}</p>
                  <Button type="primary" icon={<Upload {...ic} />} onClick={openFilePicker} style={{ marginTop: 8, ...touchSize }}>
                    {t("files.act.upload")}
                  </Button>
                </div>
              ) : (
                <p role="status" style={{ padding: "32px 16px", textAlign: "center", color: token.colorTextSecondary, border: `1px dashed ${token.colorBorder}`, borderRadius: token.borderRadiusLG }}>
                  {t("files.empty")}
                </p>
              )
            ) : (
              <>
                {/* The rows of the last read stay while the next is read: the area is busy, and its rows cannot be used (fu3b). */}
                <div
                  ref={listWrap}
                  tabIndex={-1}
                  aria-busy={busy ? "true" : undefined}
                  {...(busy ? { inert: "" } : {})}
                  // Focus in the rows is kept across a change: when the next listing loads, it goes to its first row (fu3b review 4).
                  onFocus={() => {
                    listFocused.current = true;
                  }}
                  onBlur={(e) => {
                    if (e.relatedTarget instanceof Node && !e.currentTarget.contains(e.relatedTarget)) listFocused.current = false;
                  }}
                >
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
                  onRowDoubleClick={(id) => {
                    const n = byID.get(id);
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
                  rowActs={rowActsFor}
                  onRowContext={onRowContext}
                  cutIDs={cutSet}
                  stickyTop={stickyTop(coarse)}
                  containerRef={listRef}
                />
                </div>
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

          {/* The right-click menu: the actions of the selection, or the place's own. It opens where the pointer was, and an
              antd Dropdown keeps it on the window. */}
          {menu && (
            <ConfigProvider theme={menuTheme}>
              <Dropdown
                open
                trigger={["contextMenu"]}
                autoAdjustOverflow={MENU_FIT} overlayStyle={MENU_POPUP}
                onOpenChange={(open) => {
                  if (open) return;
                  const origin = menuOrigin.current;
                  const chosen = menuChosen.current;
                  menuOrigin.current = null;
                  menuChosen.current = false;
                  setMenu(null);
                  // Closed without a choice (Escape, a click away), a menu opened from the keyboard gives the focus back to its row.
                  if (!chosen && origin?.isConnected) origin.focus();
                }}
                menu={
                  menu.scope === "space"
                    ? { items: menuItems(spaceActs), style: MENU_SCROLL }
                    : { items: groupedItems(barActs), style: MENU_SCROLL, triggerSubMenuAction: coarse ? "click" : "hover" }
                }
              >
                {/* pointer-events none: a touch's click lands on the row under the finger, not on the anchor, whose click closes the menu. */}
                <span aria-hidden="true" style={{ position: "fixed", insetInlineStart: menu.inline, insetBlockStart: menu.block, width: 1, height: 1, pointerEvents: "none" }} />
              </Dropdown>
            </ConfigProvider>
          )}

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

      {dragView && (
        <div
          ref={gestures.ghostRef}
          aria-hidden="true"
          data-testid="drag-ghost"
          data-state={dragWhy ? "refused" : dragView.target !== null ? "ok" : "free"}
          data-mode={dragView.copy ? "copy" : "move"}
          style={{
            position: "fixed",
            zIndex: 1100,
            pointerEvents: "none",
            maxInlineSize: 280,
            padding: "6px 10px",
            borderRadius: token.borderRadiusLG,
            background: token.colorBgElevated,
            color: token.colorText,
            border: `1px solid ${dragWhy ? token.colorError : token.colorPrimary}`,
            boxShadow: token.boxShadowSecondary,
            fontSize: token.fontSizeSM,
          }}
        >
          <div style={{ fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            <bdi>{dragLine}</bdi>
          </div>
          {dragView.target !== null && !dragWhy && (
            <div>
              {dragView.copy ? t("files.drag.copyTo") : t("files.drag.moveTo")} <bdi>{dragView.target}</bdi>
            </div>
          )}
          {dragHint && <div style={{ color: dragWhy ? token.colorError : token.colorTextSecondary }}>{dragHint}</div>}
        </div>
      )}

      {gestures.band && (
        <div
          ref={gestures.bandRef}
          aria-hidden="true"
          data-testid="rubber-band"
          style={{ position: "fixed", zIndex: 4, pointerEvents: "none", inlineSize: 0, blockSize: 0, boxSizing: "border-box", border: `1px solid ${token.colorPrimary}`, background: token.colorPrimaryBg, opacity: 0.6 }}
        />
      )}

      {dialog === "newFolder" && here && (
        <Suspense fallback={null}>
          <NewFolderDialog
            where={{ folder: here.folder, dirID: here.dir?.id ?? null, path: here.path }}
            openable={false}
            onClose={() => setDialog(null)}
          />
        </Suspense>
      )}

      <Modal
        open={dialog === "delete"}
        title={t("files.delete.title")}
        okText={t("files.delete.submit")}
        okButtonProps={{ danger: true }}
        cancelText={t("common.cancel")}
        onCancel={() => setDialog(null)}
        onOk={() => void confirmDelete()}
        maskClosable={false}
        destroyOnHidden
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
            void message.success(t("files.shortcut.created", { name: made.name }));
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

      {dialog === "zip" && zip && (
        <Suspense fallback={null}>
          <ZipDialog phase={zip} onClose={() => setDialog(null)} />
        </Suspense>
      )}

      {dialog === "preview" && previewItems.length > 0 && (
        <Preview
          items={previewItems}
          index={previewIndex}
          onIndex={(i) => setPreviewIndex(i)}
          onClose={(current) => {
            // The focus goes back to the row of the file on screen at close, else the row that opened the viewer, else the
            // listing itself (focusAfterClose skips a row that has gone). After the close, when the rows are still there.
            const candidates = [current?.id, previewMedia?.start.id, previewItems[previewIndex]?.id];
            requestAnimationFrame(() => focusAfterClose(listRef.current, candidates)?.focus());
            setDialog(null);
            setPreviewMedia(null);
          }}
          media={previewMedia ?? undefined}
        />
      )}

      {conflict && (
        <Conflict name={conflict.name} batch={conflict.batch} onAnswer={answerConflict} onCancel={() => answerConflict("skip", false)} />
      )}

      {undoView && <UndoToast label={undoView.label} expiresAt={undoView.expiresAt} status={undoView.status} onUndo={() => void doUndo()} onExpire={dismissUndo} />}
    </section>
    </div>
  );
}
