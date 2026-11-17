// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useSyncExternalStore } from "react";
import { ArrowDown, ArrowUp, Pause, Play, Trash2 } from "lucide-react";
import { useT } from "../../lib/i18n";
import { useMT } from "../../lib/media/i18n";
import { kindOf } from "../../lib/files/kind";
import { formatTime } from "../../lib/files/mediaKeys";
import { displayArtist, displayTitle, getTags, hasCover, loadTags, seedTags, subscribeTags, tagsVersion } from "../../lib/files/tags";
import type { Entry } from "../../lib/files/queue";
import { usePaletteOrLight } from "../../theme/theme";
import TagCover from "./TagCover";
import Listing, { type ListAction, type ListColumn } from "./Listing";
import { QueueAdd, QueueCapped, QueueHead, QueueMore } from "./MediaQueueParts";
import type { MediaQueueProps } from "./MediaQueue";

// MediaQueueTable is the queue as the Media page shows it (mp1b): a table like the drive's other lists, with the columns
// # (play order), Name and Length, and the row actions Play, Move up, Move down and Remove. It shares the drawer panel's
// head, capped note, loading-more status and add panel (MediaQueueParts) and its props (MediaQueueProps), so the two views
// keep one set of controls. Drag to reorder stays in the viewer's drawer; here a row is moved by its actions. Audio rows
// show the small cover (TagCover) and the tags' title, artist and length, as the drawer's rows do. The table is lazy
// (MediaPlaylist), so the drawer and the first load never load Listing for it.

// Row is one queue entry with its place in play order; its id is the node's, as the shared list keys rows by id.
interface Row {
  id: string;
  entry: Entry;
  index: number;
}

export default function MediaQueueTable(p: MediaQueueProps) {
  const { t } = useT();
  const mt = useMT();
  const palette = usePaletteOrLight();
  const box = useRef<HTMLElement | null>(null);
  // The audio files of the queue have their tags asked for in batches (decision 56), as the drawer's panel does. A failed
  // read keeps the file name.
  const audioIds = p.entries.filter((e) => kindOf(e.node) === "audio").map((e) => e.node.id);
  const audioKey = audioIds.join(",");
  // The rows follow every change of the tags cache.
  useSyncExternalStore(subscribeTags, tagsVersion, tagsVersion);
  useEffect(() => {
    for (const e of p.entries) seedTags(e.node.id, e.node.tags);
    if (audioIds.length > 0) void loadTags(audioIds);
    // The batch follows the queue's audio files; a change of the list is the same key.
  }, [audioKey]);

  // The row on screen is kept in view when it changes, so the table follows the file that plays.
  useEffect(() => {
    const row = box.current?.querySelector<HTMLElement>(".media-qrow--on");
    if (row && typeof row.scrollIntoView === "function") row.scrollIntoView({ block: "nearest" });
  }, [p.currentId]);

  const rows: Row[] = p.entries.map((entry, index) => ({ id: entry.node.id, entry, index }));
  const total = p.entries.length;

  // The album-page switch comes from the owner (phone width and an audio file, mp7), not the width alone: a video at phone
  // width keeps the table as it was.
  const phone = p.phone ?? false;
  const nameColumn: ListColumn<Row> = {
    key: "name",
    title: t("files.col.name"),
    render: ({ entry, id }) => {
      const on = id === p.currentId;
      const name = entry.node.name;
      const tags = getTags(id);
      if (entry.mark === "unavailable") {
        return (
          <span className="media-title--off" data-testid="media-unavailable">
            <bdi>{t("files.playlists.unavailable")}</bdi>
          </span>
        );
      }
      const shown = displayTitle(name, tags);
      // The album page's numbered list keeps the name to one line, so the artist's second line is a wider window's only.
      const artist = !phone && kindOf(entry.node) === "audio" ? displayArtist(tags) : undefined;
      return (
        <span className="media-qname">
          <span className="media-qname-line">
            {/* The whole title is in its title attribute: a cut name still reads in full on hover. */}
            <bdi dir="auto" title={shown}>
              {shown}
            </bdi>
            {/* The playing mark is an icon, with its word for a screen reader, as the drawer's row has it. */}
            <span className="media-state">
              {on &&
                (p.playing ? (
                  <>
                    <Play size={12} aria-hidden="true" />
                    <span className="sr-only">{t("files.media.playing")}</span>
                  </>
                ) : (
                  <>
                    <Pause size={12} aria-hidden="true" />
                    <span className="sr-only">{t("files.media.paused")}</span>
                  </>
                ))}
              {entry.mark === "restricted" && t("files.media.restricted")}
              {entry.mark === "failed" && t("files.media.skippedRow")}
            </span>
          </span>
          {artist !== undefined && (
            <span className="media-row-artist" dir="auto" title={artist}>
              {artist}
            </span>
          )}
        </span>
      );
    },
  };
  const posColumn: ListColumn<Row> = { key: "pos", title: "#", width: 48, render: (r) => r.index + 1 };
  const lengthColumn: ListColumn<Row> = {
    key: "length",
    title: mt.t("media.queue.length"),
    // A secondary column is the name's second line below md; the numbered list keeps the length on the name's own line.
    secondary: !phone,
    align: "end",
    width: phone ? 64 : undefined,
    render: ({ id }) => {
      const tags = getTags(id);
      // Nothing known of the length: an em dash, with no screen-reader text.
      return tags?.state === "ready" && tags.duration_ms !== undefined ? formatTime(tags.duration_ms / 1000) : "—";
    },
  };
  // At phone width the queue is the album page's numbered list (mp7): "# name length ⋮" on one line, no cover per row and
  // no second line. The # leads as the name cell and the name and length follow on the same line; every wider window keeps
  // the table as it was, the name cell with its cover and the artist under it. Listing's own defaults are unchanged.
  const columns: ListColumn<Row>[] = phone ? [posColumn, nameColumn, lengthColumn] : [nameColumn, posColumn, lengthColumn];

  const actions = (r: Row): ListAction[] => {
    const name = r.entry.node.name;
    const blocked = r.entry.mark === "restricted" || r.entry.mark === "unavailable";
    return [
      { key: "play", label: t("files.media.playThis", { name }), icon: <Play size={18} aria-hidden="true" focusable="false" />, disabled: blocked, run: () => p.onPlay(r.id) },
      { key: "up", label: t("files.media.moveUp", { name }), icon: <ArrowUp size={18} aria-hidden="true" focusable="false" />, disabled: r.index === 0, run: () => p.onMove(r.id, -1) },
      { key: "down", label: t("files.media.moveDown", { name }), icon: <ArrowDown size={18} aria-hidden="true" focusable="false" />, disabled: r.index === total - 1, run: () => p.onMove(r.id, 1) },
      { key: "remove", label: t("files.media.remove", { name }), icon: <Trash2 size={18} aria-hidden="true" focusable="false" />, danger: true, run: () => p.onRemove(r.id) },
    ];
  };

  return (
    <section ref={box} className={`media-queue media-queue--table${phone ? " media-queue--phone" : ""}`} data-testid="media-queue-table" style={{ borderColor: palette.line, color: palette.text }}>
      <QueueHead p={p} />
      <QueueCapped p={p} />
      <Listing<Row>
        items={rows}
        label={t("files.media.queue")}
        rowLabel={(r) => displayTitle(r.entry.node.name, getTags(r.id))}
        columns={columns}
        // The numbered list has no cover per row (the big cover is above the player); a wider window keeps the small cover.
        icon={
          phone
            ? undefined
            : (r) => (
                <TagCover
                  nod={r.id}
                  kind={kindOf(r.entry.node) === "video" ? "video" : "audio"}
                  size={40}
                  enabled={kindOf(r.entry.node) === "audio" && (r.entry.node.has_thumbnail === true || hasCover(r.id))}
                  node={r.entry.node}
                />
              )
        }
        actions={actions}
        onOpen={(r) => {
          if (r.entry.mark !== "restricted" && r.entry.mark !== "unavailable") p.onPlay(r.id);
        }}
        rowClassName={(r) => `media-qrow${r.id === p.currentId ? " media-qrow--on" : ""}${r.entry.mark !== "ok" ? " media-qrow--off" : ""}`}
        rowTestID={() => "media-qrow"}
        empty={{ title: t("files.media.queueEmpty"), className: "media-queue-empty" }}
        // The panel scrolls within its own height, so the column header pins to the panel's top, not the shell's.
        stickyTop={0}
      />
      <QueueMore p={p} />
      <QueueAdd p={p} />
      {p.saved}
    </section>
  );
}
