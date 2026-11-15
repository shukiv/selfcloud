// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { Alert, Button, Dropdown, theme as antdTheme } from "antd";
import { ArrowUpDown, ChevronRight, Download, FolderDown, LayoutGrid, List, Play } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import type { Node, Page } from "../../lib/files/types";
import { visitorChildren, visitorLink, visitorNode, visitorZip, visitorZipLink } from "../../lib/sharing/api";
import { childrenPage, signedLink, statNode } from "../../lib/files/api";
import { zipFailureText } from "../../lib/files/zips";
import { crumbsTo, previewKind, type NodeRef } from "../../lib/sharing/anyone";
import { kindOf } from "../../lib/files/kind";
import { Pager } from "../../lib/files/pager";
import { nextSort, sortNodes, type SortDir, type SortKey } from "../../lib/files/sort";
import { DEFAULT_PREFS, parsePrefs, type ViewPrefs } from "../../lib/files/viewPrefs";
import { isPlayable } from "../../lib/files/queue";
import { useListKeys } from "../../lib/lists";
import type { Link } from "../files/MediaPlayer";
import HeadDownload from "../files/HeadDownload";
import EmptyState from "../EmptyState";
import IconButton from "../IconButton";
import Listing, { type RowAct } from "../files/Listing";

// The player and its playlist load when a video or audio file opens.
const MediaPlaylist = lazy(() => import("../files/MediaPlaylist"));

// The visitor's view and sort are kept per browser under the share's own key, never the owner's selfcloud.files (pb1).
// Storage can be blocked, so every access is guarded and the defaults apply when nothing can be read.
const PREF_SHARE = "selfcloud.share.view";

function readSharePrefs(): ViewPrefs {
  try {
    return parsePrefs(window.localStorage.getItem(PREF_SHARE));
  } catch {
    return DEFAULT_PREFS;
  }
}

function writeSharePrefs(prefs: ViewPrefs): void {
  try {
    window.localStorage.setItem(PREF_SHARE, JSON.stringify(prefs));
  } catch {
    // Not saved; the choice lasts for this page.
  }
}

// The visitor's selection is always empty: a link's visitor selects nothing (pb1).
const NO_SELECTION: Set<string> = new Set();

// LINK_TTL is the life asked of a signed-in file's link: five minutes, enough to start the download or the preview.
const LINK_TTL = 300;

// BrowseSource is where PublicBrowse reads a node, a folder's children and a file's link from. The visitor source reads
// through the bearer credential an anyone link gives; the signed-in source reads through the drive's own calls, with the
// session the browser already holds. A source that defers its links asks for a file's contents only when the person clicks
// Download or Preview, not when the file opens.
export interface BrowseSource {
  node: (id: string) => Promise<Node>;
  children: (id: string, cursor?: string) => Promise<Page<Node>>;
  link: (id: string, disposition: "inline" | "attachment") => Promise<{ url: string }>;
  deferLinks?: boolean;
  // zip asks for a zip of the folder on screen and gives its signed download link when it is ready. Only the visitor
  // source has one: a signed-in folder is zipped from the drive's own toolbar. signal ends the wait when the visitor
  // leaves the folder or the page. expires_at, when given, is when the signed link stops working.
  zip?: (folderId: string, signal?: AbortSignal) => Promise<{ url: string; expires_at?: string }>;
}

// visitorSource reads through the visitor's calls with the bearer the page holds.
export function visitorSource(token: string): BrowseSource {
  return {
    node: (id) => visitorNode(token, id),
    children: (id, cursor) => visitorChildren(token, id, cursor),
    link: (id, disposition) => visitorLink(token, id, disposition),
    zip: (folderId, signal) => visitorZipAll(token, folderId, signal),
  };
}

// ZIP_WAIT_MS is how long the page waits for the link's zip in all: fifteen minutes, as the drive's own zip watch waits.
const ZIP_WAIT_MS = 15 * 60_000;

// visitorZipAll makes the link's zip of the folder, then asks for its signed link until the zip is ready. A zip still being
// made is asked again after the server's Retry-After, clamped to 1..10 seconds; any other refusal ends the wait, and a wait
// that runs out throws the last not-ready answer, which the page reads as slow. signal ends the wait at once: the ask in
// flight is aborted, and no further ask is made.
async function visitorZipAll(token: string, folderId: string, signal?: AbortSignal): Promise<{ url: string; expires_at: string }> {
  const zip = await visitorZip(token, folderId, signal);
  const deadline = Date.now() + ZIP_WAIT_MS;
  for (;;) {
    try {
      return await visitorZipLink(token, zip, signal);
    } catch (err) {
      const waiting = err instanceof ApiError && err.code === "conflict" && err.details.reason === "zip_not_ready";
      const wait = Math.min(10, Math.max(1, err instanceof ApiError ? err.retryAfter : 1)) * 1000;
      if (!waiting || signal?.aborted || Date.now() + wait > deadline) throw err;
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => {
          signal?.removeEventListener("abort", onAbort);
          resolve();
        }, wait);
        const onAbort = (): void => {
          clearTimeout(timer);
          reject(new ApiError(0, "cancelled", {}, 0));
        };
        signal?.addEventListener("abort", onAbort, { once: true });
      });
    }
  }
}

// startDownload starts a download of a signed link as a browser link, as downloadZip does (lib/files/zips.ts), so the
// browser resumes it if it is interrupted. The link carries no credential.
function startDownload(url: string): void {
  const a = document.createElement("a");
  a.href = url;
  a.setAttribute("download", "");
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// sessionSource reads through the drive's own calls, and defers a file's link to the click that needs it.
export function sessionSource(): BrowseSource {
  return {
    node: (id) => statNode(id),
    children: (id, cursor) => childrenPage("", id, cursor),
    link: (id, disposition) => signedLink(id, disposition, LINK_TTL),
    deferLinks: true,
  };
}

// asLink reads a link answer's address. The server's end time is not read: a player decides from what it sees, not from a
// clock (lib/files/media.ts).
function asLink(got: { url: string }): Link {
  return { url: got.url };
}

// The playlist a video or audio file opened: the file, and the folder's files read a page at a time.
interface MediaSeed {
  start: Node;
  folder: Pager;
}

// PublicBrowse shows what an anyone link opens: a folder, with its children and a path that stops at the link's root,
// or a file with a preview and a Download button. The bearer credential is held by the page and passed in; this
// component never stores it. A 401 means the credential ended, and a 404 means the link is dead: both are handed to
// the page, which decides what to do.
// A folder is listed as My files lists it (pb1): the same table and tiles, in the visitor mode of the listing, with a
// Download on every file that allows it, a Play for the folder's media, and a view and sort kept per browser.
// A video or audio file plays in the same playlist the viewer uses (MediaPlaylist), over the folder the link reaches. Its
// files are read a page at a time under the visitor's credential, and no link is asked for a file until it plays.
export interface PublicBrowseProps {
  token: string;
  root: string;
  onlost: (why: "expired" | "gone") => void;
  // source is where the reads come from; without it the visitor's way is kept, over the token given.
  source?: BrowseSource;
  // byline is a line drawn under the name, such as who shared the item and from which account.
  byline?: ReactNode;
}

export default function PublicBrowse({ token, root, onlost, source, byline }: PublicBrowseProps) {
  const { t } = useT();
  const lang = useLang();
  const { token: theme } = antdTheme.useToken();
  // The nodes the visitor has seen. A path is always made of these, since the visitor only moves down from the root,
  // so nothing above the root is ever asked for.
  const cache = useRef(new Map<string, Node>());
  // src is the source the reads go through, read at the call so a renewed token or a changed source is the one used.
  const srcRef = useRef<BrowseSource>(source ?? visitorSource(token));
  srcRef.current = source ?? visitorSource(token);
  // defer asks for a file's link only on the click that needs it, not at open.
  const defer = source?.deferLinks === true;
  const onlostRef = useRef(onlost);
  onlostRef.current = onlost;
  const aliveRef = useRef(true);
  // listing is the folder the visitor last opened, with the children read and the page after them.
  const listing = useRef<{ id: string; items: Node[]; cursor: string | null } | null>(null);
  const [current, setCurrent] = useState<Node | null>(null);
  // toggleSlot is the head row's place for the playlist button while a media file plays (MediaPlaylist).
  const [toggleSlot, setToggleSlot] = useState<HTMLElement | null>(null);
  const [trail, setTrail] = useState<NodeRef[]>([]);
  const [children, setChildren] = useState<Node[]>([]);
  const [previewLink, setPreviewLink] = useState<Link | null>(null);
  // previewFailed says a deferred preview was asked for and refused, so the page shows the no-preview line.
  const [previewFailed, setPreviewFailed] = useState(false);
  const [media, setMedia] = useState<MediaSeed | null>(null);
  // mediaNow is the playlist's file on screen, the one its Download button saves. mediaDownload is the answer the server
  // gave for that file's attachment link, asked when the file starts (as main asked at open): allowed shows the button, a
  // refusal shows the downloads-off notice, and no answer yet shows neither.
  const [mediaNow, setMediaNow] = useState<Node | null>(null);
  const [mediaDownload, setMediaDownload] = useState<{ id: string; allowed: boolean } | null>(null);
  const [downloadURL, setDownloadURL] = useState<string | null>(null);
  const [downloadRestricted, setDownloadRestricted] = useState(false);
  // The folder's zip (pz1b): zipBusy while it is made, zipReady the signed link the download started with (kept for
  // Download again until it expires), zipError the text of a refusal. zipCtl ends the wait when the visitor leaves.
  const [zipBusy, setZipBusy] = useState(false);
  const [zipReady, setZipReady] = useState<{ url: string; expiresAt: number } | null>(null);
  const [zipError, setZipError] = useState<string | null>(null);
  const zipCtlRef = useRef<AbortController | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // The view and sort, and the row the keyboard's focus is on (pb1).
  const [prefs, setPrefs] = useState<ViewPrefs>(readSharePrefs);
  const [focusID, setFocusID] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const changePrefs = (next: ViewPrefs): void => {
    setPrefs(next);
    writeSharePrefs(next);
  };

  // lost turns a refused call into the page's answer: an ended credential, or a dead link. Anything else is shown as a
  // failure here, with the one generic message.
  const lost = (err: unknown): void => {
    if (err instanceof ApiError && err.status === 401) return onlostRef.current("expired");
    if (err instanceof ApiError && err.status === 404) return onlostRef.current("gone");
    setError(t("error.generic"));
  };

  // allChildren reads the folder's pages, up to a fixed number of them, and gives the page after the last one read.
  const allChildren = async (id: string): Promise<{ items: Node[]; cursor: string | null }> => {
    const out: Node[] = [];
    let cursor: string | undefined;
    for (let page = 0; page < 50; page++) {
      const res = await srcRef.current.children(id, cursor);
      out.push(...res.items);
      if (!res.next_cursor) return { items: out, cursor: null };
      cursor = res.next_cursor;
    }
    return { items: out, cursor: cursor ?? null };
  };

  // prepareFile asks for a preview link, when the type has one, and a download link, which the server refuses when
  // no-download applies. Neither is asked for again until the file is opened again.
  const prepareFile = async (node: Node): Promise<{ preview: Link | null; download: string | null; restricted: boolean }> => {
    let preview: Link | null = null;
    if (previewKind(node.mime_type) !== null) {
      try {
        preview = asLink(await srcRef.current.link(node.id, "inline"));
      } catch (err) {
        if (err instanceof ApiError && (err.status === 401 || err.status === 404)) throw err;
        preview = null;
      }
    }
    try {
      const download = (await srcRef.current.link(node.id, "attachment")).url;
      return { preview, download, restricted: false };
    } catch (err) {
      if (err instanceof ApiError && err.code === "download_restricted") return { preview, download: null, restricted: true };
      if (err instanceof ApiError && (err.status === 401 || err.status === 404)) throw err;
      return { preview, download: null, restricted: false };
    }
  };

  // shareGone is asked by the playlist when a file's link is refused (401 or 404). It asks the share's root once: only if the
  // share itself refuses (401 or 404) is the share gone, and the page then gives its answer. A file's refusal alone is
  // that file's failure, so the playlist moves on.
  const shareGone = async (err: unknown): Promise<boolean> => {
    if (!(err instanceof ApiError) || (err.status !== 401 && err.status !== 404)) return false;
    try {
      await srcRef.current.node(root);
      return false;
    } catch (again) {
      if (again instanceof ApiError && (again.status === 401 || again.status === 404)) {
        lost(again);
        return true;
      }
      return false;
    }
  };

  // fetchLink asks for a link to a file the playlist plays: the preview link, one per file played.
  const fetchLink = (n: Node): Promise<Link> => srcRef.current.link(n.id, "inline").then(asLink);

  // onMediaCurrent is told the file the playlist plays. Its download answer is asked for now, once per file, so the page shows
  // the button or the notice for that file and no other. A source that defers its links shows the button at once and asks on
  // the click (downloadNow), so nothing is read for a file the person only plays.
  const onMediaCurrent = (n: Node | null): void => {
    setMediaNow(n);
    if (n === null || n.kind === "dir") return;
    if (defer) {
      setMediaDownload({ id: n.id, allowed: true });
      return;
    }
    void srcRef.current.link(n.id, "attachment").then(
      () => setMediaDownload({ id: n.id, allowed: true }),
      (err: unknown) => {
        if (err instanceof ApiError && err.code === "download_restricted") setMediaDownload({ id: n.id, allowed: false });
        else setMediaDownload(null);
      },
    );
  };

  // downloadNow starts a download of a file. The link is asked for on the click, not before.
  const downloadNow = async (n: Node): Promise<void> => {
    try {
      const { url } = await srcRef.current.link(n.id, "attachment");
      const a = document.createElement("a");
      a.href = url;
      a.download = "";
      a.click();
    } catch (err) {
      if (err instanceof ApiError && err.code === "download_restricted") setError(t("share.open.noDownload"));
      else lost(err);
    }
  };

  // downloadDeferred asks for a deferred file's attachment link on the click, then starts the download. A refusal of the
  // download shows the downloads-off notice; an ended credential or a dead item is handed to the page.
  const downloadDeferred = async (n: Node): Promise<void> => {
    try {
      const { url } = await srcRef.current.link(n.id, "attachment");
      const a = document.createElement("a");
      a.href = url;
      a.download = "";
      a.click();
    } catch (err) {
      if (err instanceof ApiError && err.code === "download_restricted") setDownloadRestricted(true);
      else lost(err);
    }
  };

  // previewDeferred asks for a deferred file's inline link on the click, then shows the preview. A refusal shows the
  // no-preview line; an ended credential or a dead item is handed to the page.
  const previewDeferred = async (n: Node): Promise<void> => {
    try {
      setPreviewLink(asLink(await srcRef.current.link(n.id, "inline")));
    } catch (err) {
      if (err instanceof ApiError && (err.status === 401 || err.status === 404)) {
        lost(err);
        return;
      }
      setPreviewFailed(true);
    }
  };

  // Each move gets a number. Only the latest move may set the screen, so a slow answer to an earlier click cannot
  // replace the folder the visitor picked last.
  const moveRef = useRef(0);
  const show = async (id: string): Promise<void> => {
    const move = ++moveRef.current;
    const live = (): boolean => aliveRef.current && move === moveRef.current;
    setBusy(true);
    setError(null);
    try {
      const node = cache.current.get(id) ?? (await srcRef.current.node(id));
      cache.current.set(id, node);
      if (!live()) return;
      setPreviewLink(null);
      setPreviewFailed(false);
      setMedia(null);
      // The playlist's file belongs to the file being shown: a new file clears it at once, so no render names the last one.
      setMediaNow(null);
      setMediaDownload(null);
      setDownloadURL(null);
      setDownloadRestricted(false);
      // A zip wait of the folder left behind ends here: no further ask, and no answer of it may set the screen.
      zipCtlRef.current?.abort();
      zipCtlRef.current = null;
      setZipBusy(false);
      setZipReady(null);
      setZipError(null);
      setFocusID(null);
      if (node.kind === "dir") {
        const read = await allChildren(id);
        // A move that is no longer the latest must not change what the visitor's next file is seeded from, so the listing
        // is set only once the move is known to be current.
        if (!live()) return;
        for (const c of read.items) cache.current.set(c.id, c);
        listing.current = { id, items: read.items, cursor: read.cursor };
        setChildren(read.items);
      } else {
        setChildren([]);
        const type = kindOf(node);
        if (type === "video" || type === "audio") {
          // The playlist's folder is the one the visitor came from, when it is the one on screen; the file alone otherwise.
          const trailNow = crumbsTo(cache.current, id, root) ?? [];
          const parent = trailNow.length >= 2 ? trailNow[trailNow.length - 2].id : null;
          const seen = parent !== null && listing.current?.id === parent ? listing.current : null;
          const folder = new Pager(seen?.items ?? [], seen?.cursor ?? null, (c) => srcRef.current.children(parent ?? id, c));
          if (!live()) return;
          setMedia({ start: node, folder });
        } else if (!defer) {
          const got = await prepareFile(node);
          if (!live()) return;
          setPreviewLink(got.preview);
          setDownloadURL(got.download);
          setDownloadRestricted(got.restricted);
        }
      }
      if (!live()) return;
      setCurrent(node);
      setTrail(crumbsTo(cache.current, id, root) ?? []);
    } catch (err) {
      if (live()) lost(err);
    } finally {
      if (live()) setBusy(false);
    }
  };

  useEffect(() => {
    aliveRef.current = true;
    void show(root);
    return () => {
      aliveRef.current = false;
      zipCtlRef.current?.abort();
    };
    // The first show reads the root; later moves come from the user's clicks.
  }, []);

  // startZip asks for the zip of the folder on screen, once: a second click while one is in flight does nothing. The
  // source waits for the signed link; the download starts as a browser link when it arrives. Only the latest move may
  // set the screen, so an answer that arrives after the visitor left sets nothing.
  const startZip = (): void => {
    const zip = srcRef.current.zip;
    if (!zip || current === null || current.kind !== "dir" || zipCtlRef.current !== null) return;
    const folderId = current.id;
    const move = moveRef.current;
    const live = (): boolean => aliveRef.current && move === moveRef.current;
    const ctl = new AbortController();
    zipCtlRef.current = ctl;
    setZipBusy(true);
    setZipError(null);
    setZipReady(null);
    void zip(folderId, ctl.signal).then(
      (got) => {
        if (!live()) return;
        zipCtlRef.current = null;
        setZipBusy(false);
        const expiresAt = got.expires_at !== undefined ? Date.parse(got.expires_at) : 0;
        setZipReady({ url: got.url, expiresAt: Number.isFinite(expiresAt) ? expiresAt : 0 });
        startDownload(got.url);
      },
      (err: unknown) => {
        if (!live()) return;
        zipCtlRef.current = null;
        setZipBusy(false);
        if (err instanceof ApiError && (err.status === 401 || err.status === 404)) {
          lost(err);
          return;
        }
        // A no-download answer turns downloads off for this link: the button goes with the refusal.
        if (err instanceof ApiError && err.code === "download_restricted") setDownloadRestricted(true);
        const code = err instanceof ApiError ? err.code : undefined;
        const reason = err instanceof ApiError && typeof err.details.reason === "string" ? err.details.reason : undefined;
        setZipError(zipFailureText(code, reason, lang, true));
      },
    );
  };

  // zipAgain starts the download again with the same signed link while it lives; past its end it asks for a new one
  // (the server reuses the link's zip of the same folder, so nothing is made twice).
  const zipAgain = (): void => {
    if (zipReady !== null && Date.now() < zipReady.expiresAt) {
      startDownload(zipReady.url);
      return;
    }
    startZip();
  };

  const kind = current ? previewKind(current.mime_type) : null;
  const playing = current !== null && current.kind !== "dir" && media !== null;

  // ---- The folder's listing (pb1): My files' table and tiles, in the visitor mode. ----

  // The children in the visitor's sort: folders first, then the files, each group by the key.
  const sorted = sortNodes(children, prefs.sort.key, prefs.sort.dir, lang);
  const ids = sorted.map((n) => n.id);
  const rovingID = focusID !== null && ids.includes(focusID) ? focusID : (ids[0] ?? null);

  // A plain click, a double click or Enter opens the item: a visitor selects nothing.
  const openAt = (i: number): void => {
    const n = sorted[i];
    if (n) void show(n.id);
  };

  // tileColumns is the number of tiles in one row of the grid, read from the grid the last render drew (1 until measured).
  const tileColumns = (): number => {
    const grid = listRef.current?.querySelector('[role="presentation"]');
    if (!grid) return 1;
    return Math.max(1, getComputedStyle(grid).gridTemplateColumns.split(" ").filter(Boolean).length);
  };
  const focusRow = (id: string): void => {
    setFocusID(id);
    listRef.current?.querySelector<HTMLElement>(`[data-id="${CSS.escape(id)}"]`)?.focus();
  };
  const onKey = useListKeys({
    ids,
    focusID: rovingID,
    focusRow,
    onOpen: (id) => void show(id),
    columns: prefs.view === "grid" ? tileColumns() : 1,
    dir: document.documentElement.dir === "rtl" ? "rtl" : "ltr",
  });

  // rowActs gives a file its Download icon: the attachment link is asked for on the click only (downloadNow), and a refusal
  // shows the downloads-off notice. A folder has none, and neither has a file marked no-download.
  const rowActs = (n: Node): RowAct[] =>
    n.kind === "file" && n.no_download !== true
      ? [{ key: "download", label: t("files.act.download"), icon: <Download size={18} aria-hidden="true" focusable="false" />, enabled: true, run: () => void downloadNow(n) }]
      : [];

  const hasMedia = sorted.some(isPlayable);
  // playFirst opens the first media file of the current order in the folder's playlist.
  const playFirst = (): void => {
    const first = sorted.find(isPlayable);
    if (first) void show(first.id);
  };
  // The view toggle shows the view it switches to, as My files' toolbar does.
  const switchTo = prefs.view === "list" ? { to: "grid" as const, label: t("files.view.grid") } : { to: "list" as const, label: t("files.view.list") };
  // The sort menu lists the fields, then the direction, as My files' does; choosing a field keeps the direction.
  const sortMenu = {
    selectable: true,
    selectedKeys: [`sort:${prefs.sort.key}`, `dir:${prefs.sort.dir}`],
    items: [
      { key: "sort:name", label: t("files.sort.name") },
      { key: "sort:modified", label: t("files.sort.modified") },
      { key: "sort:size", label: t("files.sort.size") },
      { type: "divider" as const, key: "sort-divider" },
      { key: "dir:asc", label: t("files.sort.asc") },
      { key: "dir:desc", label: t("files.sort.desc") },
    ],
    onClick: ({ key }: { key: string }) => {
      if (key.startsWith("sort:")) changePrefs({ ...prefs, sort: { key: key.slice(5) as SortKey, dir: prefs.sort.dir } });
      else changePrefs({ ...prefs, sort: { key: prefs.sort.key, dir: key.slice(4) as SortDir } });
    },
  };

  // The path lists only the ancestors, from the link's root down to the parent; the heading names the current place once.
  const ancestors = trail.slice(0, -1);

  return (
    <div className="public-browse">
      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBottom: 12 }} />}

      {busy && current === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>
          {t("openlink.working")}
        </p>
      )}

      {current !== null && (
        <>
          {ancestors.length > 0 && (
            <nav
              className="crumbs"
              aria-label={t("share.open.path")}
              style={{ marginBottom: 8, "--sc-crumb-ink": theme.colorTextDescription, "--sc-crumb-hover": theme.colorText } as CSSProperties}
            >
              <ol>
                {ancestors.map((crumb, i) => (
                  <li key={crumb.id}>
                    <button type="button" onClick={() => void show(crumb.id)}>
                      <bdi>{crumb.name}</bdi>
                    </button>
                    {i < ancestors.length - 1 && <ChevronRight className="icon-dir crumb-sep" size={14} aria-hidden="true" />}
                  </li>
                ))}
              </ol>
            </nav>
          )}

          {/* The heading names the file on screen: the playlist's file while one plays, the page's file otherwise. */}
          <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{(playing && mediaNow ? mediaNow : current).name}</h1>
          {byline}

          {current.kind === "dir" ? (
            <>
              {/* The toolbar row, as My files draws its own: Play, a spacer, Download all, the sort menu in tiles, and the view toggle. */}
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8 }}>
                {hasMedia && <IconButton label={t("files.act.playMedia")} icon={<Play size={16} aria-hidden="true" />} onClick={playFirst} />}
                <span style={{ flex: "1 1 auto" }} />
                {srcRef.current.zip !== undefined && !downloadRestricted && (
                  <IconButton
                    data-testid="download-all"
                    label={t("share.downloadAll")}
                    icon={<FolderDown size={16} aria-hidden="true" />}
                    loading={zipBusy}
                    disabled={zipBusy}
                    onClick={startZip}
                  />
                )}
                {prefs.view === "grid" && (
                  <Dropdown trigger={["click"]} menu={sortMenu}>
                    <IconButton label={t("files.sort.label")} icon={<ArrowUpDown size={16} aria-hidden="true" />} />
                  </Dropdown>
                )}
                <IconButton
                  data-testid="view-toggle"
                  data-view={switchTo.to}
                  label={switchTo.label}
                  icon={switchTo.to === "grid" ? <LayoutGrid size={16} aria-hidden="true" /> : <List size={16} aria-hidden="true" />}
                  onClick={() => changePrefs({ ...prefs, view: switchTo.to })}
                />
              </div>
              {zipBusy && (
                <p role="status" style={{ fontSize: 14, color: "var(--ant-color-text-secondary)", marginBottom: 8 }}>
                  {t("share.zipMaking")}
                </p>
              )}
              {zipReady !== null && (
                <p role="status" style={{ fontSize: 14, color: "var(--ant-color-text-secondary)", marginBottom: 8 }}>
                  {t("share.zipReady")}{" "}
                  <Button type="text" size="small" onClick={zipAgain}>
                    {t("files.zip.again")}
                  </Button>
                </p>
              )}
              {zipError !== null && (
                <Alert type="error" closable showIcon role="alert" message={zipError} onClose={() => setZipError(null)} style={{ marginBottom: 12 }} />
              )}
              {children.length === 0 ? (
                <EmptyState text={t("share.open.empty")} />
              ) : (
                <Listing
                  visitor
                  items={sorted}
                  view={prefs.view}
                  selected={NO_SELECTION}
                  focusID={rovingID}
                  sortKey={prefs.sort.key}
                  sortDir={prefs.sort.dir}
                  renameID={null}
                  renameValue=""
                  renameError={null}
                  label={current.name}
                  onRowClick={openAt}
                  onRowDoubleClick={(id) => void show(id)}
                  onSelectAll={() => {}}
                  onSort={(key) => changePrefs({ ...prefs, sort: nextSort(prefs.sort, key) })}
                  onRenameChange={() => {}}
                  onRename={() => {}}
                  onKeyDown={onKey}
                  onFocusItem={(i) => {
                    const id = ids[i];
                    if (id) setFocusID(id);
                  }}
                  rowActs={rowActs}
                  stickyTop={0}
                  containerRef={listRef}
                />
              )}
            </>
          ) : playing && media ? (
            <>
              {/* The head row: the playlist button (drawn by MediaPlaylist into toggleSlot) sits beside Download, as in the viewer. */}
              <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", gap: 8, marginBottom: 8 }}>
                <span ref={setToggleSlot} className="media-toggle-slot" style={{ display: "contents" }} />
                {mediaNow && mediaDownload?.id === mediaNow.id && mediaDownload.allowed && (
                  <HeadDownload label={t("files.act.download")} onClick={() => void downloadNow(mediaNow)} />
                )}
              </div>
              {mediaNow && mediaDownload?.id === mediaNow.id && !mediaDownload.allowed && (
                <p style={{ fontSize: 14, color: "var(--ant-color-text-secondary)", marginBottom: 8 }}>{t("share.open.noDownload")}</p>
              )}
              {/* The player needs a definite height: its screen, cover and video fill it. A share page has no viewer: the player
                  takes the window below the page's head (the head row, the title and the page's bar), and 360 px at least. */}
              <div style={{ display: "flex", flexDirection: "column", height: "calc(100vh - 220px)", minHeight: 360 }}>
                <Suspense fallback={<p role="status">{t("files.preview.loading")}</p>}>
                  <MediaPlaylist
                    start={media.start}
                    folder={media.folder}
                    fetchLink={fetchLink}
                    onCurrent={onMediaCurrent}
                    onLost={shareGone}
                    toggleSlot={toggleSlot}
                  />
                </Suspense>
              </div>
            </>
          ) : defer ? (
            <>
              {previewLink !== null && kind === "image" && (
                <img className="public-preview" src={previewLink.url} alt={current.name} style={{ maxWidth: "100%" }} />
              )}
              {previewLink !== null && kind === "pdf" && (
                <iframe className="public-preview" title={current.name} src={previewLink.url} style={{ width: "100%", minHeight: 480, border: 0 }} />
              )}
              {previewLink === null && (kind === null || previewFailed) && (
                <p style={{ color: "var(--ant-color-text-secondary)" }}>{t("share.open.noPreview")}</p>
              )}
              <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8, margin: "12px 0" }}>
                {kind !== null && previewLink === null && !previewFailed && (
                  <Button onClick={() => void previewDeferred(current)}>{t("files.act.preview")}</Button>
                )}
                {!downloadRestricted && (
                  <Button type="primary" onClick={() => void downloadDeferred(current)}>
                    {t("share.open.download")}
                  </Button>
                )}
              </div>
              {downloadRestricted && (
                <p style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("share.open.noDownload")}</p>
              )}
            </>
          ) : (
            <>
              {previewLink !== null && kind === "image" && (
                <img className="public-preview" src={previewLink.url} alt={current.name} style={{ maxWidth: "100%" }} />
              )}
              {previewLink !== null && kind === "pdf" && (
                <iframe className="public-preview" title={current.name} src={previewLink.url} style={{ width: "100%", minHeight: 480, border: 0 }} />
              )}
              {(previewLink === null || kind === null) && (
                <p style={{ color: "var(--ant-color-text-secondary)" }}>{t("share.open.noPreview")}</p>
              )}

              {downloadURL !== null && (
                <p style={{ margin: "12px 0" }}>
                  <a className="btn primary" href={downloadURL} download>
                    {t("share.open.download")}
                  </a>
                </p>
              )}
              {downloadURL === null && downloadRestricted && (
                <p style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("share.open.noDownload")}</p>
              )}
            </>
          )}
        </>
      )}
    </div>
  );
}
