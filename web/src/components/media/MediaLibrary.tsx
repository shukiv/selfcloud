// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page's library tabs (mp4): Videos and Music list the media files the caller reaches through the search,
// Recently played lists the caller's own played files. One tab draws on the shared preset list; a row's click or Enter
// plays it in the page's session with the loaded rows as the selection, and the page turns back to Now playing. The
// search always carries the tab's type filter: a library tab never sends a search with no filter. Search off keeps the
// table's frame with the Empty inside it (the owner's rule); a rate limit shows its text and a retry. The audio rows
// have their tags asked in batches, as the queue does (decision 56).

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Alert, Button, Skeleton } from "antd";
import { FolderOpen, Play } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { useDriveText } from "../../lib/drive/driveText";
import { locationOf } from "../../lib/drive/location";
import { useLocationSpaces } from "../../lib/drive/spaces";
import { fileErrorText } from "../../lib/files/fileErrors";
import { formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import { useLoadMoreSentinel } from "../../lib/files/loadMoreSentinel";
import { formatTime } from "../../lib/files/mediaKeys";
import { Pager } from "../../lib/files/pager";
import { startPlayback } from "../../lib/files/playback";
import { filesURL } from "../../lib/files/route";
import { displayArtist, displayTitle, getTags, hasCover, loadTags, seedTags, subscribeTags, tagsVersion } from "../../lib/files/tags";
import type { Node, Page } from "../../lib/files/types";
import { useLang, useT } from "../../lib/i18n";
import { listPlayed, type PlayedItem } from "../../lib/media/api";
import { useMT } from "../../lib/media/i18n";
import { useCoarsePointer } from "../../lib/pointer";
import { searchNodes } from "../../lib/search/api";
import { session, sessionKey } from "../../lib/session";
import { useStore } from "../../lib/store";
import DriveTextFailure from "../drive/DriveTextFailure";
import Icon from "../files/Icon";
import Listing, { type ListColumn, type ListEmpty } from "../files/Listing";
import TagCover from "../files/TagCover";

// LibraryTab is one of the Media page's library tabs.
export type LibraryTab = "videos" | "music" | "recent";

// Row is a file of any library tab: a search hit, or a played file with the place the person stopped at.
type Row = Node & Partial<Pick<PlayedItem, "position_seconds" | "duration_seconds" | "played_at">>;

// PAGE is how many rows one ask reads.
const PAGE = 50;

// The keys of each tab's own texts: its label, and its empty state's title and description.
const TAB_KEY: Record<LibraryTab, string> = { videos: "media.tab.videos", music: "media.tab.music", recent: "media.tab.recent" };
const EMPTY_KEY: Record<LibraryTab, { title: string; body: string }> = {
  videos: { title: "media.lib.empty.videos.title", body: "media.lib.empty.videos.body" },
  music: { title: "media.lib.empty.music.title", body: "media.lib.empty.music.body" },
  recent: { title: "media.lib.empty.recent.title", body: "media.lib.empty.recent.body" },
};

// readPage asks one page of the tab's list: the search with the tab's type filter (a file of that group), or the played
// list, whose every place is a media file already. The cursor continues the same ask.
function readPage(tab: LibraryTab, cursor: string | null): Promise<Page<Row>> {
  const p = new URLSearchParams();
  if (tab !== "recent") {
    p.set("type", tab === "videos" ? "video" : "audio");
    p.set("kind", "file");
  }
  p.set("limit", String(PAGE));
  if (cursor !== null) p.set("cursor", cursor);
  return tab === "recent" ? listPlayed(p) : searchNodes(p);
}

// mergeRows appends a page to the rows shown, leaving out a row that is already there (as the search page does).
function mergeRows(shown: Row[], page: Row[]): Row[] {
  const seen = new Set(shown.map((r) => r.id));
  return [...shown, ...page.filter((r) => !seen.has(r.id))];
}

// emptyPager is the folder of a library play: the selection is the list, so the folder has nothing to read.
function emptyPager(): Pager {
  return new Pager([], null, async () => ({ items: [] }));
}

// albumOf reads the album of the tags' answer, when one is there.
function albumOf(n: Row): string | undefined {
  const tags = getTags(n.id);
  return tags?.state === "ready" ? tags.album : undefined;
}

// lengthOf writes the tags' length as m:ss, or a dash when it is not known.
function lengthOf(n: Row): string {
  const tags = getTags(n.id);
  const ms = tags?.state === "ready" ? tags.duration_ms : undefined;
  return ms === undefined ? "—" : formatTime(ms / 1000);
}

export default function MediaLibrary({ tab, onPlay }: { tab: LibraryTab; onPlay: () => void }) {
  const { t, ready } = useMT();
  const { t: mt } = useT();
  const lang = useLang();
  const coarse = useCoarsePointer();
  const me = useStore(session);
  const sk = sessionKey(me);
  const spaces = useLocationSpaces(me !== null);
  const drive = useDriveText();
  const navigate = useNavigate();

  const [items, setItems] = useState<Row[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<unknown>(null);
  // moreFailed: the last next page failed, so the sentinel waits for Load more.
  const [moreFailed, setMoreFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  // seq numbers each ask: an answer that is not the latest one is dropped.
  const seq = useRef(0);

  const load = useCallback(
    async (reset: boolean, from: string | null) => {
      const mine = ++seq.current;
      if (reset) {
        setItems([]);
        setCursor(null);
        setFailure(null);
        setMoreFailed(false);
      }
      setLoading(true);
      try {
        const page = await readPage(tab, from);
        if (mine !== seq.current) return;
        setItems((prev) => (reset ? page.items : mergeRows(prev, page.items)));
        setCursor(page.next_cursor ?? null);
        setFailure(null);
        if (!reset) setMoreFailed(false);
      } catch (err) {
        if (mine !== seq.current) return;
        // A failed next page is not asked for again by the sentinel; Load more tries again.
        if (!reset) setMoreFailed(true);
        setFailure(err);
      } finally {
        if (mine === seq.current) setLoading(false);
      }
    },
    [tab],
  );

  // The tab reads once when it opens, and again for a new person or a retry; never on a render.
  useEffect(() => {
    void load(true, null);
  }, [sk, load, attempt]);

  const loadMore = () => {
    if (cursor === null || loading) return;
    void load(false, cursor);
  };

  const retry = () => {
    if (moreFailed) loadMore();
    else setAttempt((n) => n + 1);
  };

  // The audio rows have their tags asked in batches (decision 56), as the queue does. A failed read keeps the file name.
  const audioIds = items.filter((n) => kindOf(n) === "audio").map((n) => n.id);
  const audioKey = audioIds.join(",");
  // The rows follow every change of the tags cache.
  useSyncExternalStore(subscribeTags, tagsVersion, tagsVersion);
  useEffect(() => {
    for (const n of items) seedTags(n.id, n.tags);
    if (audioIds.length > 0) void loadTags(audioIds);
    // The batch follows the list's audio files; a change of the list is the same key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [audioKey]);

  // play starts the page's session with the row and the loaded rows as the selection, then turns to Now playing. The
  // resume place applies by itself: the player reads it.
  const play = (n: Row) => {
    startPlayback({ start: n, folder: emptyPager(), selection: items });
    onPlay();
  };

  // openFolder goes to the folder the file sits in: its parent, unless the parent is the space's root, which the
  // folder's own address shows (as planNodeLink decides).
  const openFolder = (n: Row) => {
    const root = spaces.find((f) => f.id === n.folder_id)?.root_node_id;
    const dir = n.parent_id !== null && n.parent_id !== root ? n.parent_id : null;
    navigate(filesURL(n.folder_id, dir));
  };

  const sentinel = useLoadMoreSentinel(cursor !== null, loading, moreFailed, loadMore);

  // searchOff is the search's own refusal: the library needs it, and the table says so inside its frame.
  const searchOff = tab !== "recent" && failure instanceof ApiError && failure.code === "not_found";
  const failureMessage =
    failure === null || searchOff
      ? null
      : failure instanceof ApiError && failure.code === "rate_limited"
        ? t("media.lib.rateLimited")
        : fileErrorText(failure, lang);

  const columns: ListColumn<Row>[] =
    tab === "videos"
      ? [
          { key: "name", title: mt("files.col.name"), render: (n) => <bdi>{n.name}</bdi> },
          { key: "folder", title: t("media.lib.col.folder"), secondary: true, render: (n) => locationOf(n, spaces, mt("nav.files")) },
          { key: "modified", title: mt("files.col.modified"), secondary: true, render: (n) => <time dateTime={n.modified_at}>{formatDate(n.modified_at, lang)}</time> },
        ]
      : tab === "music"
        ? [
            {
              key: "name",
              title: mt("files.col.name"),
              // The whole title is in the title attribute too: a cut name still reads in full on hover, as the queue's.
              render: (n) => (
                <bdi dir="auto" title={n.name}>
                  {displayTitle(n.name, getTags(n.id))}
                </bdi>
              ),
            },
            { key: "artist", title: t("media.lib.col.artist"), secondary: true, render: (n) => displayArtist(getTags(n.id)) ?? "—" },
            { key: "album", title: t("media.lib.col.album"), secondary: true, render: (n) => albumOf(n) ?? "—" },
            { key: "length", title: t("media.lib.col.length"), secondary: true, align: "end", render: (n) => lengthOf(n) },
          ]
        : [
            {
              key: "name",
              title: mt("files.col.name"),
              render: (n) => (
                <bdi dir="auto" title={n.name}>
                  {kindOf(n) === "audio" ? displayTitle(n.name, getTags(n.id)) : n.name}
                </bdi>
              ),
            },
            {
              key: "played",
              title: t("media.lib.col.played"),
              secondary: true,
              ariaSort: "descending",
              render: (n) => <time dateTime={n.played_at}>{formatDate(n.played_at, lang)}</time>,
            },
            {
              key: "progress",
              title: t("media.lib.col.progress"),
              secondary: true,
              render: (n) => {
                const pos = formatTime(n.position_seconds ?? 0);
                const dur = n.duration_seconds ?? 0;
                return dur > 0 ? t("media.lib.progressOf", { position: pos, duration: formatTime(dur) }) : pos;
              },
            },
          ];

  // The row's icon: the kind's own for a video, the cover box for an audio file (40 pixels, as the music rows are).
  const icon = (n: Row) =>
    tab === "videos" ? (
      <Icon kind={kindOf(n)} size={24} node={n} />
    ) : (
      <TagCover nod={n.id} kind={kindOf(n) === "audio" ? "audio" : "video"} size={40} enabled={kindOf(n) === "audio" && (n.has_thumbnail === true || hasCover(n.id))} node={n} />
    );

  const empty: ListEmpty = searchOff
    ? { title: t("media.lib.searchOff.title"), description: t("media.lib.searchOff.body"), className: "empty" }
    : {
        title: t(EMPTY_KEY[tab].title),
        description: t(EMPTY_KEY[tab].body),
        action: { label: t("media.empty.action"), onClick: () => navigate("/files") },
        className: "empty",
      };

  // The page's own strings wait for their catalogue, as the page itself does.
  if (!ready) return <Skeleton active />;

  return (
    <div>
      {drive.failed && <DriveTextFailure retry={drive.retry} />}

      {failureMessage !== null && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={failureMessage}
          action={
            <Button size="small" onClick={retry} disabled={loading}>
              {mt("files.retry")}
            </Button>
          }
          style={{ marginBlockEnd: 12 }}
        />
      )}

      {((!drive.ready && !drive.failed) || (loading && items.length === 0)) && failure === null && <Skeleton active />}

      {drive.ready && (items.length > 0 || searchOff || (!loading && failure === null)) && (
        <div className="items">
          <Listing<Row>
            label={t(TAB_KEY[tab])}
            items={items}
            rowLabel={(n) => n.name}
            columns={columns}
            icon={icon}
            actions={(n) => [
              { key: "play", label: `${mt("files.act.play")}: ${n.name}`, icon: <Play size={18} aria-hidden="true" focusable="false" />, run: () => play(n) },
              { key: "folder", label: `${t("media.lib.openFolder")}: ${n.name}`, icon: <FolderOpen size={18} aria-hidden="true" focusable="false" />, run: () => openFolder(n) },
            ]}
            onOpen={play}
            empty={empty}
          />
        </div>
      )}

      {cursor !== null && <div ref={sentinel} aria-hidden="true" />}
      {cursor !== null && (
        <div style={{ marginBlockStart: 12 }}>
          <Button onClick={loadMore} disabled={loading} style={coarse ? { minHeight: 44 } : undefined}>
            {mt("files.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}
