// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useSyncExternalStore, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { Alert, Button, Dropdown, type MenuProps } from "antd";
import IconButton from "../IconButton";
import { ArrowDown, ArrowUp, GripVertical, ListMusic, MoreHorizontal, Pause, Play, Plus, Repeat, Repeat1, Shuffle, Trash2 } from "lucide-react";
import { useT } from "../../lib/i18n";
import { kindOf } from "../../lib/files/kind";
import { displayArtist, displayTitle, getTags, hasCover, loadTags, seedTags, subscribeTags, tagsVersion } from "../../lib/files/tags";
import TagCover from "./TagCover";
import { MAX_QUEUE, type Entry, type Repeat as RepeatMode } from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";
import { usePaletteOrLight } from "../../theme/theme";
import EmptyState from "../EmptyState";

// MediaQueue is the playlist panel: the files in play order, the one on screen marked, and the actions on the list.
// A row is one line at every width: a grip to drag it, its name (cut with an ellipsis, the full name on hover), the mark
// of the file on screen, and a More menu with Move up, Move down and Remove. Alt with the arrow keys on a focused row moves
// it too. The row on screen is kept in view. The panel's colours come from the palette, so they follow the page's theme.

interface Props {
  entries: Entry[];
  currentId: string | null;
  repeat: RepeatMode;
  shuffle: boolean;
  capped: boolean;
  // moreLeft says the folder has more files to read; the queue reads them as it reaches the end.
  moreLeft: boolean;
  busy: boolean;
  // candidates are the folder's playable files that are not in the queue, for the add panel.
  candidates: Node[];
  adding: boolean;
  // playing says the file on screen plays now. A current row that is not playing says Paused, not Playing.
  playing?: boolean;
  onAddOpen: () => void;
  onAdd: (node: Node, where: "next" | "end") => void;
  onLoadMore: () => void;
  onPlay: (id: string) => void;
  onRemove: (id: string) => void;
  onMove: (id: string, delta: number) => void;
  onShuffle: () => void;
  onRepeat: () => void;
  // saved is the saved-playlists section under the list (MediaPlaylists), which needs the list's state and so is given here.
  saved?: ReactNode;
}

export default function MediaQueue(p: Props) {
  const { t } = useT();
  const palette = usePaletteOrLight();
  const drag = useRef<string | null>(null);
  // The audio files of the queue have their tags asked for in batches (decision 56). A failed read keeps the file name.
  const audioIds = p.entries.filter((e) => kindOf(e.node) === "audio").map((e) => e.node.id);
  const audioKey = audioIds.join(",");
  // The rows follow every change of the tags cache.
  useSyncExternalStore(subscribeTags, tagsVersion, tagsVersion);
  useEffect(() => {
    for (const e of p.entries) seedTags(e.node.id, e.node.tags);
    if (audioIds.length > 0) void loadTags(audioIds);
    // The batch follows the queue's audio files; a change of the list is the same key.
  }, [audioKey]);
  const list = useRef<HTMLOListElement | null>(null);
  const repeatLabel = p.repeat === "all" ? t("files.media.repeatAll") : p.repeat === "one" ? t("files.media.repeatOne") : t("files.media.repeatOff");
  const RepeatIcon = p.repeat === "one" ? Repeat1 : Repeat;

  // The row on screen is kept in view when it changes, so the list follows the file that plays.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>('[aria-current="true"]');
    if (row && typeof row.scrollIntoView === "function") row.scrollIntoView({ block: "nearest" });
  }, [p.currentId]);

  // A grip drag moves the row one place at a time, toward the row under the pointer. The pointer is read on the window,
  // not captured by the grip: a row that moves in the list moves its grip with it, and a captured grip would lose the drag.
  const latest = useRef(p);
  latest.current = p;
  useEffect(() => {
    const move = (e: MouseEvent) => {
      const id = drag.current;
      if (id === null) return;
      const row = document.elementFromPoint(e.clientX, e.clientY)?.closest<HTMLElement>("[data-row-index]");
      if (!row) return;
      const to = Number(row.dataset.rowIndex);
      const from = latest.current.entries.findIndex((x) => x.node.id === id);
      if (from >= 0 && to !== from) latest.current.onMove(id, to > from ? 1 : -1);
    };
    const up = () => {
      drag.current = null;
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
  }, []);
  const onGripDown = (e: PointerEvent<HTMLButtonElement>, id: string) => {
    drag.current = id;
    e.preventDefault();
  };

  // Alt with the arrow keys moves the focused row, and keeps the focus on it.
  const onRowKey = (e: KeyboardEvent<HTMLLIElement>, id: string) => {
    if (!e.altKey || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
    e.preventDefault();
    e.stopPropagation();
    p.onMove(id, e.key === "ArrowUp" ? -1 : 1);
  };

  const total = p.entries.length;

  return (
    <section className="media-queue" aria-labelledby="media-queue-title" data-testid="media-queue" style={{ borderColor: palette.line, color: palette.text }}>
      <header className="media-queue-head">
        <h3 id="media-queue-title" className="media-queue-title">
          <ListMusic size={16} aria-hidden="true" /> {t("files.media.queue")}
        </h3>
        <div className="media-queue-actions">
          <IconButton label={t("files.media.shuffle")} pressed={p.shuffle} onClick={p.onShuffle} icon={<Shuffle size={18} aria-hidden="true" focusable="false" />} />
          <IconButton label={`${t("files.media.repeat")}: ${repeatLabel}`} pressed={p.repeat !== "off"} onClick={p.onRepeat} icon={<RepeatIcon size={18} aria-hidden="true" focusable="false" />} />
          <IconButton label={t("files.media.addFiles")} aria-expanded={p.adding} onClick={p.onAddOpen} icon={<Plus size={18} aria-hidden="true" focusable="false" />} />
        </div>
      </header>

      {p.capped && <Alert type="info" showIcon message={t("files.media.capped", { count: MAX_QUEUE })} style={{ marginBottom: 8 }} />}

      {total === 0 ? (
        <EmptyState size="compact" className="media-queue-empty" text={t("files.media.queueEmpty")} />
      ) : (
        <ol className="media-rows" ref={list}>
          {p.entries.map((entry, index) => {
            const id = entry.node.id;
            const on = id === p.currentId;
            const name = entry.node.name;
            const items: MenuProps["items"] = [
              { key: "up", label: t("files.media.moveUp", { name }), icon: <ArrowUp size={14} aria-hidden="true" />, disabled: index === 0 },
              { key: "down", label: t("files.media.moveDown", { name }), icon: <ArrowDown size={14} aria-hidden="true" />, disabled: index === total - 1 },
              { type: "divider" },
              { key: "remove", danger: true, label: t("files.media.remove", { name }), icon: <Trash2 size={14} aria-hidden="true" /> },
            ];
            const onMenu: MenuProps["onClick"] = ({ key }) => {
              if (key === "up") p.onMove(id, -1);
              else if (key === "down") p.onMove(id, 1);
              else if (key === "remove") p.onRemove(id);
            };
            return (
              <li
                key={id}
                className={`media-row${on ? " media-row--on" : ""}${entry.mark !== "ok" ? " media-row--off" : ""}`}
                style={on ? { background: palette.accentSoft, color: palette.text } : undefined}
                data-row-index={index}
                data-testid="media-row"
                aria-current={on ? "true" : undefined}
                onKeyDown={(e) => onRowKey(e, id)}
              >
                <button
                  type="button"
                  className="media-grip"
                  aria-label={t("files.media.drag", { name })}
                  onPointerDown={(e) => onGripDown(e, id)}
                >
                  <GripVertical size={16} aria-hidden="true" />
                </button>
                {/* The box is there from the first render, with its icon, so a cover or a tag that arrives moves nothing. */}
                <TagCover
                  nod={id}
                  kind={kindOf(entry.node) === "video" ? "video" : "audio"}
                  size={28}
                  enabled={kindOf(entry.node) === "audio" && (entry.node.has_thumbnail === true || hasCover(id))}
                  node={entry.node}
                />
                {entry.mark === "unavailable" ? (
                  <span className="media-title media-title--off" data-testid="media-unavailable">
                    <bdi>{t("files.playlists.unavailable")}</bdi>
                  </span>
                ) : (
                <button
                  type="button"
                  className="media-title"
                  title={name}
                  aria-current={on ? "true" : undefined}
                  disabled={entry.mark === "restricted"}
                  onClick={() => p.onPlay(id)}
                >
                  {/* The whole title and artist are in their title attributes: a cut name still reads in full on hover. */}
                  <bdi dir="auto" title={displayTitle(name, getTags(id))}>
                    {displayTitle(name, getTags(id))}
                  </bdi>
                  {displayArtist(getTags(id)) !== undefined && (
                    <span className="media-row-artist" dir="auto" title={displayArtist(getTags(id))}>
                      {displayArtist(getTags(id))}
                    </span>
                  )}
                </button>
                )}
                {/* The playing mark is an icon, with its word for a screen reader: the word would otherwise take the room a title
                    needs on a phone. */}
                <span className="media-state" style={{ color: on ? palette.text : palette.muted }}>
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
                <span className="media-row-buttons">
                  <Dropdown menu={{ items, onClick: onMenu }} trigger={["click"]}>
                    <IconButton className="media-btn" label={t("files.media.more", { name })} tip={t("files.selection.more")} icon={<MoreHorizontal size={16} aria-hidden="true" focusable="false" />} />
                  </Dropdown>
                </span>
              </li>
            );
          })}
        </ol>
      )}

      {p.busy && (
        <p role="status" className="media-queue-more">
          {t("files.media.loadingMore")}
        </p>
      )}

      {p.saved}

      {p.adding && (
        <div className="media-add" data-testid="media-add">
          {p.candidates.length === 0 ? (
            <EmptyState size="compact" text={t("files.media.addNone")} />
          ) : (
            <ul className="media-rows">
              {p.candidates.map((n) => (
                <li key={n.id} className="media-row">
                  <span className="media-title">
                    <bdi>{n.name}</bdi>
                  </span>
                  <span className="media-row-buttons">
                    <Button size="small" onClick={() => p.onAdd(n, "next")} aria-label={`${t("files.media.addNext")}: ${n.name}`}>
                      {t("files.media.addNext")}
                    </Button>
                    <Button size="small" onClick={() => p.onAdd(n, "end")} aria-label={`${t("files.media.addEnd")}: ${n.name}`}>
                      {t("files.media.addEnd")}
                    </Button>
                  </span>
                </li>
              ))}
            </ul>
          )}
          {p.moreLeft && (
            <Button size="small" onClick={p.onLoadMore} disabled={p.busy}>
              {t("files.media.loadMore")}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
