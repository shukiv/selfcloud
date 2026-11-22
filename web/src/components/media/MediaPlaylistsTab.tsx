// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page's Playlists tab: the person's saved playlists and the two automatic lists, Continue watching and Recently
// added, side by side on a desktop and stacked on a phone. The left table lists Continue watching first (its Automatic tag
// and first-page count), then Recently added (the last 30 days' video and audio files, newest first), then the saved ones;
// the address's list= picks the detail. The detail plays its available rows through the page's one session, shows the place
// and the time left for Continue watching and the added date for Recently added, and edits a saved playlist (rename, delete,
// move, remove, add files) only on the person's action. An unavailable entry is italic, tagged, and never played. New
// playlist names an empty playlist through the queue save's name rules; Add files opens the lazy file picker on My files and
// adds the chosen media in the order they were checked, without duplicating an entry.

import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { Alert, App, Button, Grid, Input, Modal, Popconfirm, Skeleton, Tag } from "antd";
import { FilePlus, History, ListMusic, ListPlus, Play, Shuffle } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { locationOf } from "../../lib/drive/location";
import { useLocationSpaces } from "../../lib/drive/spaces";
import { fileErrorText } from "../../lib/files/fileErrors";
import { formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import { useLoadMoreSentinel } from "../../lib/files/loadMoreSentinel";
import { formatTime } from "../../lib/files/mediaKeys";
import { Pager } from "../../lib/files/pager";
import { getPlayback, startPlayback, subscribePlayback } from "../../lib/files/playback";
import {
  createPlaylist,
  deletePlaylist,
  getPlaylist,
  listPlaylists,
  removeEntry,
  renamePlaylist,
  reorderEntries,
  replaceEntries,
  type PlaylistDetail,
  type PlaylistSummary,
} from "../../lib/files/playlists";
import { getQueueControl, subscribeQueueControl } from "../../lib/files/queueControl";
import { filesURL } from "../../lib/files/route";
import { getTags, loadTags, seedTags, subscribeTags, tagsVersion } from "../../lib/files/tags";
import type { Node } from "../../lib/files/types";
import { useLang, useT } from "../../lib/i18n";
import { listPlayed, type PlayedItem } from "../../lib/media/api";
import { useMT } from "../../lib/media/i18n";
import { useCoarsePointer } from "../../lib/pointer";
import { searchNodes, type SearchHit } from "../../lib/search/api";
import { session, sessionKey } from "../../lib/session";
import { useStore } from "../../lib/store";
import Icon from "../files/Icon";
import IconButton from "../IconButton";
import Listing, { type ListColumn, type ListEmpty, type ListMenuEntry } from "../files/Listing";

// The file picker is a lazy chunk of its own, so it is not in the tab's first load.
const PickFiles = lazy(() => import("../files/PickFiles"));

// NAME_MAX is the longest a playlist's name may be, as the queue's save and rename dialogs keep it.
const NAME_MAX = 120;
// ENTRIES_MAX is the most files a playlist holds, as the server keeps it; Add files offers the room left.
const ENTRIES_MAX = 500;
// mediaAccept keeps the audio and video files alone: a directory, a shortcut or any other kind is not a playlist's file.
const mediaAccept = (n: Node) => {
  const k = kindOf(n);
  return k === "audio" || k === "video";
};
// PAGE is how many rows one ask of an automatic list reads; Continue watching's left count says 50+ while a next page is there.
const PAGE = 50;
// CONTINUE is the automatic list's place in the address's list=, and the default when it names nothing the person has.
const CONTINUE = "continue";
// RECENT is the second automatic list's place in the address's list=: the files created in the last RECENT_DAYS days.
const RECENT = "recent";
const RECENT_DAYS = 30;

// LeftRow is one row of the left table: an automatic list (Continue watching or Recently added), or a saved playlist.
interface LeftRow {
  id: string;
  name: string;
  automatic: boolean;
  count: string;
  updated: string | null;
}

// DetailRow is one row of the detail: a played file with its place, a recently added file, or a playlist entry. An
// unavailable entry has no node.
interface DetailRow {
  id: string;
  entryId: string | null;
  node: Node | null;
  unavailable: boolean;
  index: number;
  position: number;
  duration: number;
}

// emptyPager is the folder of a playlist play: the selection is the list, so the folder has nothing to read.
function emptyPager(): Pager {
  return new Pager([], null, async () => ({ items: [] }));
}

// mergeRows appends a page to the rows shown, leaving out a row that is already there.
function mergeRows<T extends { id: string }>(shown: T[], page: T[]): T[] {
  const seen = new Set(shown.map((r) => r.id));
  return [...shown, ...page.filter((r) => !seen.has(r.id))];
}

export default function MediaPlaylistsTab({ onPlay }: { onPlay: () => void }) {
  const { t, ready } = useMT();
  const { t: mt } = useT();
  const lang = useLang();
  const coarse = useCoarsePointer();
  const me = useStore(session);
  const sk = sessionKey(me);
  const spaces = useLocationSpaces(me !== null);
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const screens = Grid.useBreakpoint();
  const phone = !screens.md;
  const { message } = App.useApp();

  // The session and its queue control: Play next is offered only while a session runs and its queue can be reached.
  const snap = useSyncExternalStore(subscribePlayback, getPlayback, getPlayback);
  const control = useSyncExternalStore(subscribeQueueControl, getQueueControl, getQueueControl);
  const canPlayNext = snap.seed !== null && control !== null;

  // The saved playlists of the left table.
  const [lists, setLists] = useState<PlaylistSummary[] | null>(null);
  const [listsErr, setListsErr] = useState<unknown>(null);
  const [listsTick, setListsTick] = useState(0);

  // Continue watching: the played list, paged. The left count is its first page; the detail reads on.
  const [played, setPlayed] = useState<PlayedItem[]>([]);
  const [playedCursor, setPlayedCursor] = useState<string | null>(null);
  const [playedLoading, setPlayedLoading] = useState(true);
  const [playedFirstDone, setPlayedFirstDone] = useState(false);
  const [playedErr, setPlayedErr] = useState<unknown>(null);
  const [playedMoreFailed, setPlayedMoreFailed] = useState(false);
  const [playedTick, setPlayedTick] = useState(0);
  const playedSeq = useRef(0);

  // Recently added: the newest video and audio files of the last 30 days, paged. The left count is its first page; the
  // next pages are read only while the list is chosen.
  const [recent, setRecent] = useState<SearchHit[]>([]);
  const [recentCursor, setRecentCursor] = useState<string | null>(null);
  const [recentLoading, setRecentLoading] = useState(true);
  const [recentFirstDone, setRecentFirstDone] = useState(false);
  const [recentErr, setRecentErr] = useState<unknown>(null);
  const [recentMoreFailed, setRecentMoreFailed] = useState(false);
  const [recentTick, setRecentTick] = useState(0);
  const recentSeq = useRef(0);

  // The detail of a saved playlist.
  const [detail, setDetail] = useState<PlaylistDetail | null>(null);
  const [detailErr, setDetailErr] = useState<unknown>(null);
  const [detailTick, setDetailTick] = useState(0);

  const [renameOpen, setRenameOpen] = useState(false);
  const [renameName, setRenameName] = useState("");
  const [renameBusy, setRenameBusy] = useState(false);

  // New playlist: the name dialog, and the one create on its way. Add files: the lazy picker's open state.
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createBusy, setCreateBusy] = useState(false);
  const createInFlight = useRef(false);
  const [addOpen, setAddOpen] = useState(false);
  // Add files waits for its replace and the detail's re-read: while they run the button is off and another pick sends
  // nothing, so a second Add cannot build on the old entries and drop the first batch.
  const [addBusy, setAddBusy] = useState(false);
  const addInFlight = useRef(false);

  useEffect(() => {
    let live = true;
    listPlaylists().then(
      (got) => {
        if (!live) return;
        setLists(got);
        setListsErr(null);
      },
      (err) => {
        if (live) setListsErr(err);
      },
    );
    return () => {
      live = false;
    };
  }, [sk, listsTick]);

  const loadPlayed = useCallback(async (reset: boolean, from: string | null) => {
    const mine = ++playedSeq.current;
    if (reset) {
      setPlayed([]);
      setPlayedCursor(null);
      setPlayedErr(null);
      setPlayedMoreFailed(false);
    }
    setPlayedLoading(true);
    try {
      const p = new URLSearchParams();
      p.set("unfinished", "true");
      p.set("limit", String(PAGE));
      if (from !== null) p.set("cursor", from);
      const page = await listPlayed(p);
      if (mine !== playedSeq.current) return;
      setPlayed((prev) => (reset ? page.items : mergeRows(prev, page.items)));
      setPlayedCursor(page.next_cursor ?? null);
      setPlayedErr(null);
      if (!reset) setPlayedMoreFailed(false);
    } catch (err) {
      if (mine !== playedSeq.current) return;
      if (!reset) setPlayedMoreFailed(true);
      setPlayedErr(err);
    } finally {
      if (mine === playedSeq.current) {
        setPlayedLoading(false);
        if (reset) setPlayedFirstDone(true);
      }
    }
  }, []);

  useEffect(() => {
    void loadPlayed(true, null);
  }, [sk, loadPlayed, playedTick]);

  const loadRecent = useCallback(async (reset: boolean, from: string | null) => {
    const mine = ++recentSeq.current;
    if (reset) {
      setRecent([]);
      setRecentCursor(null);
      setRecentErr(null);
      setRecentMoreFailed(false);
    }
    setRecentLoading(true);
    try {
      const p = new URLSearchParams();
      p.append("type", "video");
      p.append("type", "audio");
      p.set("kind", "file");
      p.set("order", "newest");
      p.set("limit", String(PAGE));
      p.set("created_after", String(Date.now() - RECENT_DAYS * 24 * 60 * 60 * 1000));
      if (from !== null) p.set("cursor", from);
      const page = await searchNodes(p);
      if (mine !== recentSeq.current) return;
      setRecent((prev) => (reset ? page.items : mergeRows(prev, page.items)));
      setRecentCursor(page.next_cursor ?? null);
      setRecentErr(null);
      if (!reset) setRecentMoreFailed(false);
    } catch (err) {
      if (mine !== recentSeq.current) return;
      if (!reset) setRecentMoreFailed(true);
      setRecentErr(err);
    } finally {
      if (mine === recentSeq.current) {
        setRecentLoading(false);
        if (reset) setRecentFirstDone(true);
      }
    }
  }, []);

  useEffect(() => {
    void loadRecent(true, null);
  }, [sk, loadRecent, recentTick]);

  // The address's list= picks the detail: Recently added, a saved playlist the person has, else Continue watching. An
  // unknown id falls back without rewriting the address.
  const listParam = params.get("list");
  const selected =
    listParam === RECENT
      ? RECENT
      : listParam !== null && listParam !== CONTINUE && lists !== null && lists.some((l) => l.id === listParam)
        ? listParam
        : CONTINUE;
  const isContinue = selected === CONTINUE;
  const isRecent = selected === RECENT;
  const selectedList = lists?.find((l) => l.id === selected);

  useEffect(() => {
    // The re-read landing is what frees Add files: the replace alone is not enough, the detail must hold the new entries.
    const doneAdd = () => {
      if (addInFlight.current) {
        addInFlight.current = false;
        setAddBusy(false);
      }
    };
    if (isContinue || isRecent) {
      setDetail(null);
      setDetailErr(null);
      doneAdd();
      return;
    }
    let live = true;
    getPlaylist(selected).then(
      (d) => {
        if (!live) return;
        setDetail(d);
        setDetailErr(null);
        doneAdd();
      },
      (err) => {
        if (!live) return;
        setDetailErr(err);
        doneAdd();
      },
    );
    return () => {
      live = false;
    };
  }, [selected, isContinue, isRecent, sk, detailTick]);

  // select writes the chosen list in the address with replace; Continue watching keeps the address clean.
  const select = (id: string) => {
    setParams(id === CONTINUE ? { tab: "playlists" } : { tab: "playlists", list: id }, { replace: true });
  };

  const detailRows: DetailRow[] = useMemo(() => {
    if (isContinue) {
      return played.map((n, i) => ({ id: n.id, entryId: null, node: n, unavailable: false, index: i, position: n.position_seconds, duration: n.duration_seconds }));
    }
    if (isRecent) {
      return recent.map((n, i) => ({ id: n.id, entryId: null, node: n, unavailable: false, index: i, position: 0, duration: 0 }));
    }
    if (detail === null) return [];
    return detail.entries.map((e, i) => ({
      id: e.entry_id,
      entryId: e.entry_id,
      node: e.unavailable ? null : (e.node ?? null),
      unavailable: e.unavailable || !e.node,
      index: i,
      position: 0,
      duration: 0,
    }));
  }, [isContinue, isRecent, played, recent, detail]);

  const availableNodes = useMemo(() => detailRows.filter((r) => !r.unavailable && r.node !== null).map((r) => r.node as Node), [detailRows]);

  // The detail's media rows have their tags asked in batches, as the library does, for the length of a saved file.
  const audioIds = detailRows.filter((r) => r.node !== null && kindOf(r.node) === "audio").map((r) => (r.node as Node).id);
  const audioKey = audioIds.join(",");
  useSyncExternalStore(subscribeTags, tagsVersion, tagsVersion);
  useEffect(() => {
    for (const r of detailRows) if (r.node !== null) seedTags(r.node.id, r.node.tags);
    if (audioIds.length > 0) void loadTags(audioIds);
    // The batch follows the detail's audio files; a change of the detail is the same key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [audioKey]);

  const playFrom = (n: Node) => {
    startPlayback({ start: n, folder: emptyPager(), selection: availableNodes });
    onPlay();
  };
  const playAll = () => {
    if (availableNodes.length > 0) playFrom(availableNodes[0]!);
  };
  const shuffleAll = () => {
    if (availableNodes.length === 0) return;
    // A shuffled round starts at a random available row, not always the first, dealt as the queue's own shuffle deals it.
    const start = availableNodes[Math.floor(Math.random() * availableNodes.length)]!;
    startPlayback({ start, folder: emptyPager(), selection: availableNodes, shuffle: true });
    onPlay();
  };

  const openFolder = (n: Node) => {
    const root = spaces.find((f) => f.id === n.folder_id)?.root_node_id;
    const dir = n.parent_id !== null && n.parent_id !== root ? n.parent_id : null;
    navigate(filesURL(n.folder_id, dir));
  };

  const move = (r: DetailRow, dir: -1 | 1) => {
    if (detail === null || r.entryId === null) return;
    const ids = detail.entries.map((e) => e.entry_id);
    const i = ids.indexOf(r.entryId);
    const j = i + dir;
    if (i < 0 || j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j]!, ids[i]!];
    reorderEntries(selected, ids).then(
      () => setDetailTick((n) => n + 1),
      () => message.error(mt("files.playlists.failed")),
    );
  };
  const remove = (r: DetailRow) => {
    if (r.entryId === null) return;
    removeEntry(selected, r.entryId).then(
      () => setDetailTick((n) => n + 1),
      () => message.error(mt("files.playlists.failed")),
    );
  };

  const openRename = () => {
    setRenameName(detail?.name ?? selectedList?.name ?? "");
    setRenameOpen(true);
  };
  const saveRename = async () => {
    const trimmed = renameName.trim();
    if (trimmed.length === 0 || trimmed.length > NAME_MAX) return;
    setRenameBusy(true);
    try {
      await renamePlaylist(selected, trimmed);
      setRenameOpen(false);
      setListsTick((n) => n + 1);
      setDetailTick((n) => n + 1);
    } catch {
      message.error(mt("files.playlists.failed"));
    } finally {
      setRenameBusy(false);
    }
  };
  const del = () => {
    deletePlaylist(selected).then(
      () => setListsTick((n) => n + 1),
      () => message.error(mt("files.playlists.failed")),
    );
  };

  // New playlist makes an empty playlist with the name the dialog keeps, once, and opens it. A second press while the create
  // runs makes no second playlist; a refusal leaves the dialog for a retry.
  const openCreate = () => {
    setCreateName("");
    setCreateOpen(true);
  };
  const saveCreate = async () => {
    const trimmed = createName.trim();
    if (trimmed.length === 0 || trimmed.length > NAME_MAX) return;
    if (createInFlight.current) return;
    createInFlight.current = true;
    setCreateBusy(true);
    try {
      const made = await createPlaylist(trimmed, []);
      setCreateOpen(false);
      setListsTick((n) => n + 1);
      select(made.id);
    } catch {
      message.error(mt("files.playlists.failed"));
    } finally {
      createInFlight.current = false;
      setCreateBusy(false);
    }
  };

  // Add files sets the playlist to its entries then the chosen files, in the order they were checked, leaving out a file the
  // playlist already holds, in one request, and re-reads the detail. A second Add waits: while the replace and its re-read
  // run, another pick returns at once, so it cannot send the old entries again and drop the first batch.
  const addFiles = (nodes: Node[]) => {
    setAddOpen(false);
    if (detail === null || addInFlight.current) return;
    addInFlight.current = true;
    setAddBusy(true);
    const seen = new Set<string>();
    const ids: string[] = [];
    for (const e of detail.entries) {
      if (!seen.has(e.node_id)) {
        seen.add(e.node_id);
        ids.push(e.node_id);
      }
    }
    for (const n of nodes) {
      if (!seen.has(n.id)) {
        seen.add(n.id);
        ids.push(n.id);
      }
    }
    replaceEntries(selected, ids).then(
      () => setDetailTick((n) => n + 1),
      () => {
        message.error(mt("files.playlists.failed"));
        addInFlight.current = false;
        setAddBusy(false);
      },
    );
  };

  const loadMore = () => {
    if (playedCursor === null || playedLoading) return;
    void loadPlayed(false, playedCursor);
  };
  const sentinel = useLoadMoreSentinel(isContinue && playedCursor !== null, playedLoading, playedMoreFailed, loadMore);
  const loadMoreRecent = () => {
    if (recentCursor === null || recentLoading) return;
    void loadRecent(false, recentCursor);
  };
  const recentSentinel = useLoadMoreSentinel(isRecent && recentCursor !== null, recentLoading, recentMoreFailed, loadMoreRecent);

  const detailName = isContinue ? t("media.pl.continue") : isRecent ? t("media.pl.recent") : (detail?.name ?? selectedList?.name ?? "");
  const kind = isContinue || isRecent ? t("media.pl.kindAutomatic") : t("media.pl.kindSaved");
  const rowCount = detailRows.length;
  // The detail's entry count drives Add files: the room left for the picker, and the cap that turns the button off.
  const entryCount = detail?.entry_count ?? selectedList?.entry_count ?? 0;
  const atCap = entryCount >= ENTRIES_MAX;
  const personalId = spaces.find((s) => s.kind === "personal")?.id;

  const rowName = (r: DetailRow) => r.node?.name ?? "";
  const menu = (r: DetailRow): ListMenuEntry[] => {
    const entries: ListMenuEntry[] = [];
    if (!r.unavailable && r.node !== null) {
      const n = r.node;
      entries.push({ key: "play", label: t("media.pl.playFromHere"), onSelect: () => playFrom(n) });
      if (canPlayNext) entries.push({ key: "next", label: mt("files.media.addNext"), onSelect: () => getQueueControl()?.addNext(n) });
      entries.push({ key: "folder", label: t("media.pl.openInFolder"), onSelect: () => openFolder(n) });
    }
    if (!isContinue && r.entryId !== null) {
      entries.push({ key: "up", label: mt("files.playlists.moveUp", { name: rowName(r) }), disabled: r.index === 0, onSelect: () => move(r, -1) });
      entries.push({ key: "down", label: mt("files.playlists.moveDown", { name: rowName(r) }), disabled: r.index === rowCount - 1, onSelect: () => move(r, 1) });
      entries.push({ key: "remove", label: mt("files.playlists.remove", { name: rowName(r) }), danger: true, onSelect: () => remove(r) });
    }
    return entries;
  };

  const lengthOf = (r: DetailRow): string => {
    if (r.duration > 0) return formatTime(r.duration);
    const tags = r.node !== null ? getTags(r.node.id) : undefined;
    const ms = tags?.state === "ready" ? tags.duration_ms : undefined;
    return ms === undefined ? "—" : formatTime(ms / 1000);
  };

  const titleCell = (r: DetailRow) => {
    if (r.unavailable || r.node === null) return <span className="media-title--off">{t("media.pl.unavailable")}</span>;
    return (
      <span style={{ minWidth: 0 }}>
        <bdi dir="auto" title={r.node.name}>
          {r.node.name}
        </bdi>
        {isContinue && r.duration > 0 && (
          <span className="media-pl-place">
            {t("media.pl.at", { time: formatTime(r.position) })} · {t("media.pl.left", { time: formatTime(r.duration - r.position) })}
          </span>
        )}
      </span>
    );
  };

  const detailColumns: ListColumn<DetailRow>[] = [
    { key: "title", title: t("media.pl.colTitle"), render: titleCell },
    { key: "num", title: "#", width: 48, render: (r) => r.index + 1 },
    { key: "folder", title: t("media.lib.col.folder"), secondary: true, render: (r) => (r.node !== null ? locationOf(r.node, spaces, mt("nav.files")) : "—") },
    isRecent
      ? { key: "added", title: t("media.pl.colAdded"), secondary: true, render: (r) => (r.node !== null ? <time dateTime={r.node.created_at}>{formatDate(r.node.created_at, lang)}</time> : "—") }
      : { key: "length", title: t("media.lib.col.length"), secondary: true, align: "end", render: lengthOf },
  ];

  const continueCount = playedCursor !== null ? `${PAGE}+` : String(played.length);
  const leftRows: LeftRow[] = [
    { id: CONTINUE, name: t("media.pl.continue"), automatic: true, count: continueCount, updated: played[0]?.played_at ?? null },
    { id: RECENT, name: t("media.pl.recent"), automatic: true, count: String(recent.length), updated: recent[0]?.created_at ?? null },
    ...(lists ?? []).map((l) => ({ id: l.id, name: l.name, automatic: false, count: String(l.entry_count), updated: l.updated_at })),
  ];
  const leftColumns: ListColumn<LeftRow>[] = [
    {
      key: "name",
      title: mt("files.col.name"),
      render: (r) =>
        r.automatic ? (
          <span style={{ display: "inline-flex", alignItems: "center", gap: 8, minWidth: 0 }}>
            <bdi>{r.name}</bdi>
            <Tag>{t("media.pl.automatic")}</Tag>
          </span>
        ) : (
          <bdi>{r.name}</bdi>
        ),
    },
    { key: "items", title: t("media.pl.colItems"), secondary: true, render: (r) => r.count },
    { key: "updated", title: t("media.pl.colUpdated"), secondary: true, render: (r) => (r.updated !== null ? <time dateTime={r.updated}>{formatDate(r.updated, lang)}</time> : "—") },
  ];

  const summaryCount = isContinue ? continueCount : isRecent ? String(recent.length) : String(detail?.entry_count ?? selectedList?.entry_count ?? 0);
  const summaryDate = isContinue ? (played[0]?.played_at ?? null) : isRecent ? (recent[0]?.created_at ?? null) : (detail?.updated_at ?? selectedList?.updated_at ?? null);
  const summary = summaryDate !== null ? t("media.pl.summary", { count: summaryCount, date: formatDate(summaryDate, lang) }) : t("media.pl.summaryCount", { count: summaryCount });

  const continueEmpty: ListEmpty = {
    title: t("media.pl.emptyContinueTitle"),
    description: t("media.pl.emptyContinueBody"),
    action: { label: t("media.pl.emptyContinueAction"), onClick: () => setParams({ tab: "videos" }, { replace: true }) },
    className: "empty",
  };
  const recentEmpty: ListEmpty = {
    title: t("media.pl.emptyRecentTitle"),
    className: "empty",
  };
  const savedEmpty: ListEmpty = {
    title: t("media.pl.emptySavedTitle"),
    description: t("media.pl.emptySavedBody"),
    action: { label: t("media.pl.emptySavedAction"), onClick: () => navigate("/files") },
    className: "empty",
  };

  // The page's own strings wait for their catalogue, and the tables for the first reads, as the library does.
  if (!ready) return <Skeleton active />;
  if (lists === null && listsErr !== null) {
    return (
      <Alert
        type="error"
        showIcon
        role="alert"
        message={fileErrorText(listsErr, lang)}
        action={
          <Button size="small" onClick={() => setListsTick((n) => n + 1)}>
            {mt("files.retry")}
          </Button>
        }
      />
    );
  }
  if (lists === null || !playedFirstDone || !recentFirstDone) return <Skeleton active />;

  const detailBody = () => {
    if (isContinue) {
      if (playedErr !== null && played.length === 0) {
        return (
          <Alert
            type="error"
            showIcon
            role="alert"
            message={fileErrorText(playedErr, lang)}
            action={
              <Button size="small" onClick={() => setPlayedTick((n) => n + 1)}>
                {mt("files.retry")}
              </Button>
            }
          />
        );
      }
      return (
        <Listing<DetailRow>
          label={detailName}
          items={detailRows}
          rowLabel={(r) => rowName(r)}
          columns={detailColumns}
          icon={(r) => (r.node !== null ? <Icon kind={kindOf(r.node)} size={22} node={r.node} /> : null)}
          menu={menu}
          rowClassName={(r) => (r.unavailable ? "media-pl-row--off" : "")}
          onOpen={(r) => {
            if (!r.unavailable && r.node !== null) playFrom(r.node);
          }}
          empty={continueEmpty}
        />
      );
    }
    if (isRecent) {
      if (recentErr !== null && recent.length === 0) {
        return (
          <Alert
            type="error"
            showIcon
            role="alert"
            message={recentErr instanceof ApiError && recentErr.code === "rate_limited" ? t("media.lib.rateLimited") : fileErrorText(recentErr, lang)}
            action={
              <Button size="small" onClick={() => setRecentTick((n) => n + 1)}>
                {mt("files.retry")}
              </Button>
            }
          />
        );
      }
      return (
        <Listing<DetailRow>
          label={detailName}
          items={detailRows}
          rowLabel={(r) => rowName(r)}
          columns={detailColumns}
          icon={(r) => (r.node !== null ? <Icon kind={kindOf(r.node)} size={22} node={r.node} /> : null)}
          menu={menu}
          rowClassName={(r) => (r.unavailable ? "media-pl-row--off" : "")}
          onOpen={(r) => {
            if (!r.unavailable && r.node !== null) playFrom(r.node);
          }}
          empty={recentEmpty}
        />
      );
    }
    if (detailErr !== null) {
      return (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={fileErrorText(detailErr, lang)}
          action={
            <Button size="small" onClick={() => setDetailTick((n) => n + 1)}>
              {mt("files.retry")}
            </Button>
          }
        />
      );
    }
    if (detail === null) return <Skeleton active />;
    return (
      <Listing<DetailRow>
        label={detailName}
        items={detailRows}
        rowLabel={(r) => rowName(r)}
        columns={detailColumns}
        icon={(r) => (r.node !== null ? <Icon kind={kindOf(r.node)} size={22} node={r.node} /> : null)}
        menu={menu}
        rowClassName={(r) => (r.unavailable ? "media-pl-row--off" : "")}
        onOpen={(r) => {
          if (!r.unavailable && r.node !== null) playFrom(r.node);
        }}
        empty={savedEmpty}
      />
    );
  };

  return (
    <div className={`media-pl-layout${phone ? " media-pl-layout--phone" : ""}`} data-testid="media-pl-layout">
      <div className="media-pl-list">
        <header className="media-queue-head">
          <h3 className="media-queue-title">
            <ListMusic size={16} aria-hidden="true" focusable="false" /> {t("media.pl.yourPlaylists")}
          </h3>
          <div className="media-queue-actions">
            <IconButton label={t("media.pl.newPlaylist")} disabled={createBusy} onClick={openCreate} icon={<ListPlus size={18} aria-hidden="true" focusable="false" />} />
          </div>
        </header>
        <Listing<LeftRow>
          label={t("media.pl.yourPlaylists")}
          items={leftRows}
          rowLabel={(r) => r.name}
          columns={leftColumns}
          icon={(r) => (r.automatic ? <History size={22} aria-hidden="true" focusable="false" /> : <ListMusic size={22} aria-hidden="true" focusable="false" />)}
          rowClassName={(r) => (r.id === selected ? "media-pl-row--on" : "")}
          onOpen={(r) => select(r.id)}
          empty={{ title: t("media.pl.continue") }}
        />
      </div>

      <div className="media-pl-detail">
        <div className="media-pl-head">
          <div className="media-pl-head-text">
            <h2 className="media-pl-name">{detailName}</h2>
            <Tag>{kind}</Tag>
            <span className="media-pl-summary">{summary}</span>
          </div>
          <div className="media-pl-actions">
            <Button type="primary" icon={<Play size={16} aria-hidden="true" focusable="false" />} disabled={availableNodes.length === 0} onClick={playAll}>
              {mt("files.act.play")}
            </Button>
            <Button icon={<Shuffle size={16} aria-hidden="true" focusable="false" />} disabled={availableNodes.length === 0} onClick={shuffleAll}>
              {t("media.pl.shufflePlay")}
            </Button>
            {!isContinue && !isRecent && (
              <IconButton
                label={t("media.pl.addFiles")}
                tip={atCap ? t("media.pl.full") : undefined}
                disabled={detail === null || atCap || addBusy}
                onClick={() => setAddOpen(true)}
                icon={<FilePlus size={16} aria-hidden="true" focusable="false" />}
              />
            )}
            {!isContinue && !isRecent && (
              <Button onClick={openRename} disabled={detail === null}>
                {mt("files.playlists.rename")}
              </Button>
            )}
            {!isContinue && !isRecent && (
              <Popconfirm
                title={t("media.pl.deleteConfirm")}
                okText={mt("files.playlists.deleteOk")}
                cancelText={mt("files.playlists.cancel")}
                onConfirm={del}
              >
                <Button danger disabled={detail === null}>
                  {mt("files.playlists.delete")}
                </Button>
              </Popconfirm>
            )}
          </div>
        </div>

        {detailBody()}

        {isContinue && playedCursor !== null && <div ref={sentinel} aria-hidden="true" />}
        {isRecent && recentCursor !== null && <div ref={recentSentinel} aria-hidden="true" />}
        {isContinue && playedCursor !== null && (
          <div style={{ marginBlockStart: 12 }}>
            <Button onClick={loadMore} disabled={playedLoading} style={coarse ? { minHeight: 44 } : undefined}>
              {mt("files.loadMore")}
            </Button>
          </div>
        )}
        {isRecent && recentCursor !== null && (
          <div style={{ marginBlockStart: 12 }}>
            <Button onClick={loadMoreRecent} disabled={recentLoading} style={coarse ? { minHeight: 44 } : undefined}>
              {mt("files.loadMore")}
            </Button>
          </div>
        )}
      </div>

      <Modal
        open={renameOpen}
        title={mt("files.playlists.renameTitle")}
        okText={mt("files.playlists.save")}
        cancelText={mt("files.playlists.cancel")}
        okButtonProps={{ disabled: renameName.trim().length === 0 || renameName.trim().length > NAME_MAX, loading: renameBusy }}
        onOk={() => void saveRename()}
        onCancel={() => setRenameOpen(false)}
        maskClosable={false}
        destroyOnClose
      >
        <label className="pl-field">
          <span>{mt("files.playlists.nameLabel")}</span>
          <Input value={renameName} maxLength={NAME_MAX} onChange={(e) => setRenameName(e.target.value)} onPressEnter={() => void saveRename()} />
        </label>
        <p className="pl-hint">{mt("files.playlists.nameHint", { count: NAME_MAX })}</p>
      </Modal>

      <Modal
        open={createOpen}
        title={t("media.pl.newPlaylist")}
        okText={t("media.pl.create")}
        cancelText={mt("files.playlists.cancel")}
        okButtonProps={{ disabled: createName.trim().length === 0 || createName.trim().length > NAME_MAX, loading: createBusy }}
        onOk={() => void saveCreate()}
        onCancel={() => setCreateOpen(false)}
        maskClosable={false}
        destroyOnClose
      >
        <label className="pl-field">
          <span>{mt("files.playlists.nameLabel")}</span>
          <Input value={createName} maxLength={NAME_MAX} onChange={(e) => setCreateName(e.target.value)} onPressEnter={() => void saveCreate()} />
        </label>
        <p className="pl-hint">{mt("files.playlists.nameHint", { count: NAME_MAX })}</p>
      </Modal>

      {addOpen && !isContinue && !isRecent && (
        <Suspense fallback={null}>
          <PickFiles
            title={t("media.pl.addTitle", { name: detailName })}
            action={t("media.pl.add")}
            spaces={spaces}
            startAt={personalId}
            accept={mediaAccept}
            multiple
            max={ENTRIES_MAX - entryCount}
            onPick={addFiles}
            onClose={() => setAddOpen(false)}
          />
        </Suspense>
      )}
    </div>
  );
}