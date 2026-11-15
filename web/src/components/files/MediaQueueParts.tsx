// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button } from "antd";
import IconButton from "../IconButton";
import { ListMusic, Plus, Repeat, Repeat1, Shuffle } from "lucide-react";
import { useT } from "../../lib/i18n";
import { MAX_QUEUE } from "../../lib/files/queue";
import EmptyState from "../EmptyState";
import type { MediaQueueProps } from "./MediaQueue";

// The parts of the queue both views share (mp1b): the drawer's panel (MediaQueue) and the Media page's table
// (MediaQueueTable) draw the same head, capped note, loading-more status and add panel, so the two keep one set of
// controls and one markup. Each takes the queue's whole props, as the panel had them.

// QueueHead is the title with its icon and the Shuffle, Repeat and Add files buttons, with their pressed and expanded
// states.
export function QueueHead({ p }: { p: MediaQueueProps }) {
  const { t } = useT();
  const repeatLabel = p.repeat === "all" ? t("files.media.repeatAll") : p.repeat === "one" ? t("files.media.repeatOne") : t("files.media.repeatOff");
  const RepeatIcon = p.repeat === "one" ? Repeat1 : Repeat;
  return (
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
  );
}

// QueueCapped is the note that the queue holds no more files.
export function QueueCapped({ p }: { p: MediaQueueProps }) {
  const { t } = useT();
  if (!p.capped) return null;
  return <Alert type="info" showIcon message={t("files.media.capped", { count: MAX_QUEUE })} style={{ marginBottom: 8 }} />;
}

// QueueMore is the loading-more status.
export function QueueMore({ p }: { p: MediaQueueProps }) {
  const { t } = useT();
  if (!p.busy) return null;
  return (
    <p role="status" className="media-queue-more">
      {t("files.media.loadingMore")}
    </p>
  );
}

// QueueAdd is the add panel: the folder's playable files that are not in the queue, each added next or to the end.
export function QueueAdd({ p }: { p: MediaQueueProps }) {
  const { t } = useT();
  if (!p.adding) return null;
  return (
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
  );
}
