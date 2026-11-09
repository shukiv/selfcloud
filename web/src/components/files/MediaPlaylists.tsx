// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Alert, App, Button, Input, Modal, Popconfirm } from "antd";
import IconButton from "../IconButton";
import { ArrowDown, ArrowUp, ListMusic, ListOrdered, Pencil, Play, Save, Trash2, X } from "lucide-react";
import { useT } from "../../lib/i18n";
import { kindOf } from "../../lib/files/kind";
import { displayTitle, getTags, loadTags, seedTags, subscribeTags, tagsVersion } from "../../lib/files/tags";
import type { Entry } from "../../lib/files/queue";
import EmptyState from "../EmptyState";
import {
  createPlaylist,
  deletePlaylist,
  entriesOf,
  getPlaylist,
  listPlaylists,
  removeEntry,
  renamePlaylist,
  reorderEntries,
  savedNodes,
  type PlaylistDetail,
  type PlaylistSummary,
} from "../../lib/files/playlists";

// MediaPlaylists is the saved-playlists section under the queue (decision 58). It lists the person's playlists, saves the
// queue as a new one, and opens one in the queue. Each playlist can be renamed, deleted (after a confirmation), or edited:
// its files can be removed and reordered, and an unavailable one is shown as such. A failed refresh keeps the list on
// screen; a failed first load shows an error with a retry. Names and artists come from the audio tags, as queue rows do.

const NAME_MAX = 120;

interface Props {
  // queue is the queue on screen, saved when the person saves it.
  queue: Entry[];
  // onOpen puts a playlist's entries in place of the queue.
  onOpen: (entries: Entry[]) => void;
}

export default function MediaPlaylists({ queue, onOpen }: Props) {
  const { t, tp } = useT();
  const { message } = App.useApp();
  const [lists, setLists] = useState<PlaylistSummary[] | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [saveOpen, setSaveOpen] = useState(false);
  const [renaming, setRenaming] = useState<PlaylistSummary | null>(null);

  // load reads the list. A first failure is shown as an error; a later one keeps the list that is on screen.
  // have is true once a list has been read, so a later failure keeps it (a ref: the answer is read when the failure comes).
  const have = useRef(false);
  const load = async () => {
    try {
      const got = await listPlaylists();
      have.current = true;
      setLists(got);
      setLoadFailed(false);
    } catch {
      if (!have.current) setLoadFailed(true);
      else message.error(t("files.playlists.refreshFailed"));
    }
  };
  // The list is read once when the section mounts; a retry reads it again.
  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the list is read once on mount
  }, []);

  const failed = () => message.error(t("files.playlists.failed"));

  // openSeq numbers the opens. An answer that is not the latest press's is dropped, so the newest Play wins even when the
  // earlier read answers last.
  const openSeq = useRef(0);
  const open = async (pl: PlaylistSummary) => {
    const mine = ++openSeq.current;
    try {
      const detail = await getPlaylist(pl.id);
      if (mine !== openSeq.current) return;
      onOpen(entriesOf(detail));
      message.success(t("files.playlists.opened"));
    } catch {
      if (mine === openSeq.current) failed();
    }
  };

  const remove = async (pl: PlaylistSummary) => {
    try {
      await deletePlaylist(pl.id);
      if (editing === pl.id) setEditing(null);
      await load();
    } catch {
      failed();
    }
  };

  const queueNodes = savedNodes(queue);

  return (
    <section className="media-playlists" aria-labelledby="media-playlists-title" data-testid="media-playlists">
      <header className="media-queue-head">
        <h3 id="media-playlists-title" className="media-queue-title">
          <ListMusic size={16} aria-hidden="true" /> {t("files.playlists.title")}
        </h3>
        <div className="media-queue-actions">
          <IconButton label={t("files.playlists.saveQueue")} disabled={queueNodes.length === 0} className="pl-touch" onClick={() => setSaveOpen(true)} icon={<Save size={18} aria-hidden="true" focusable="false" />} />
        </div>
      </header>

      {loadFailed && lists === null && (
        <Alert
          type="error"
          showIcon
          message={t("files.playlists.loadFailed")}
          action={
            <Button size="small" className="pl-touch" onClick={() => void load()}>
              {t("files.playlists.retry")}
            </Button>
          }
        />
      )}

      {lists !== null && lists.length === 0 && <EmptyState size="compact" className="media-queue-empty" text={t("files.playlists.none")} />}

      {lists !== null && lists.length > 0 && (
        <ul className="media-rows" data-testid="playlist-list">
          {lists.map((pl) => (
            <li key={pl.id} className="media-row" data-testid="playlist-row">
              <span className="media-title">
                <bdi dir="auto">{pl.name}</bdi>
                <span className="media-row-artist">{tp("files.playlists.count", pl.entry_count)}</span>
              </span>
              <span className="media-row-buttons">
                <Button size="small" className="pl-touch" aria-label={`${t("files.playlists.play")}: ${pl.name}`}
                  icon={<Play size={14} aria-hidden="true" />} onClick={() => void open(pl)}>
                  {t("files.playlists.play")}
                </Button>
                <Button size="small" className="pl-touch" aria-expanded={editing === pl.id}
                  aria-label={`${t("files.playlists.edit")}: ${pl.name}`} icon={<ListOrdered size={14} aria-hidden="true" />}
                  onClick={() => setEditing(editing === pl.id ? null : pl.id)}>
                  {t("files.playlists.edit")}
                </Button>
                <Button size="small" className="pl-touch" aria-label={`${t("files.playlists.rename")}: ${pl.name}`}
                  icon={<Pencil size={14} aria-hidden="true" />} onClick={() => setRenaming(pl)} />
                <Popconfirm
                  title={t("files.playlists.deleteConfirm")}
                  okText={t("files.playlists.deleteOk")}
                  cancelText={t("files.playlists.cancel")}
                  okButtonProps={{ danger: true }}
                  onConfirm={() => void remove(pl)}
                >
                  <Button size="small" danger className="pl-touch" aria-label={`${t("files.playlists.delete")}: ${pl.name}`}
                    icon={<Trash2 size={14} aria-hidden="true" />} />
                </Popconfirm>
              </span>
              {editing === pl.id && <PlaylistEditor pls={pl.id} name={pl.name} onChanged={() => void load()} onClose={() => setEditing(null)} />}
            </li>
          ))}
        </ul>
      )}

      <SaveDialog
        open={saveOpen}
        nodes={queueNodes}
        onClose={() => setSaveOpen(false)}
        onSaved={() => {
          setSaveOpen(false);
          message.success(t("files.playlists.saved"));
          void load();
        }}
        onFailed={failed}
      />
      <RenameDialog
        target={renaming}
        onClose={() => setRenaming(null)}
        onRenamed={() => {
          setRenaming(null);
          void load();
        }}
        onFailed={failed}
      />
    </section>
  );
}

// SaveDialog names a new playlist and fills it with the queue's files. The name is checked here as the server checks it.
function SaveDialog(p: { open: boolean; nodes: string[]; onClose: () => void; onSaved: () => void; onFailed: () => void }) {
  const { t } = useT();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  // inFlight is read before a second press can render: a second Enter or click while a save runs does nothing.
  const inFlight = useRef(false);
  const trimmed = name.trim();
  const ok = trimmed.length > 0 && trimmed.length <= NAME_MAX;
  const save = async () => {
    if (!ok || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    try {
      // One request creates the playlist with its files: a refusal leaves no playlist, so the retry makes the only one.
      await createPlaylist(trimmed, p.nodes);
      setName("");
      p.onSaved();
    } catch {
      p.onFailed();
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };
  return (
    <Modal
      open={p.open}
      title={t("files.playlists.saveTitle")}
      okText={t("files.playlists.save")}
      cancelText={t("files.playlists.cancel")}
      okButtonProps={{ disabled: !ok, loading: busy }}
      onOk={() => void save()}
      onCancel={p.onClose}
      maskClosable={false}
      destroyOnClose
    >
      <label className="pl-field">
        <span>{t("files.playlists.nameLabel")}</span>
        <Input value={name} maxLength={NAME_MAX} onChange={(e) => setName(e.target.value)} onPressEnter={() => void save()} />
      </label>
      <p className="pl-hint">{t("files.playlists.nameHint", { count: NAME_MAX })}</p>
    </Modal>
  );
}

// RenameDialog changes a playlist's name.
function RenameDialog(p: { target: PlaylistSummary | null; onClose: () => void; onRenamed: () => void; onFailed: () => void }) {
  const { t } = useT();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (p.target) setName(p.target.name);
  }, [p.target]);
  const trimmed = name.trim();
  const ok = trimmed.length > 0 && trimmed.length <= NAME_MAX;
  const save = async () => {
    if (!p.target || !ok) return;
    setBusy(true);
    try {
      await renamePlaylist(p.target.id, trimmed);
      p.onRenamed();
    } catch {
      p.onFailed();
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open={p.target !== null}
      title={t("files.playlists.renameTitle")}
      okText={t("files.playlists.save")}
      cancelText={t("files.playlists.cancel")}
      okButtonProps={{ disabled: !ok, loading: busy }}
      onOk={() => void save()}
      onCancel={p.onClose}
      maskClosable={false}
      destroyOnClose
    >
      <label className="pl-field">
        <span>{t("files.playlists.nameLabel")}</span>
        <Input value={name} maxLength={NAME_MAX} onChange={(e) => setName(e.target.value)} onPressEnter={() => void save()} />
      </label>
      <p className="pl-hint">{t("files.playlists.nameHint", { count: NAME_MAX })}</p>
    </Modal>
  );
}

// PlaylistEditor shows a playlist's entries, with remove and reorder. An unavailable entry shows as such, and can be removed.
function PlaylistEditor(p: { pls: string; name: string; onChanged: () => void; onClose: () => void }) {
  const { t } = useT();
  const { message } = App.useApp();
  const [detail, setDetail] = useState<PlaylistDetail | null>(null);
  const [failed, setFailed] = useState(false);
  const have = useRef(false);
  useSyncExternalStore(subscribeTags, tagsVersion, tagsVersion);

  const read = async () => {
    try {
      const d = await getPlaylist(p.pls);
      have.current = true;
      setDetail(d);
      setFailed(false);
      const audio: string[] = [];
      for (const e of d.entries) {
        if (!e.node) continue;
        seedTags(e.node.id, e.node.tags);
        if (kindOf(e.node) === "audio") audio.push(e.node.id);
      }
      if (audio.length > 0) void loadTags(audio);
    } catch {
      // A failed refresh keeps the rows on screen; a failed first read shows the error.
      if (!have.current) setFailed(true);
      else message.error(t("files.playlists.refreshFailed"));
    }
  };
  useEffect(() => {
    void read();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- read once for the playlist shown
  }, [p.pls]);

  // change runs one edit and reads the list again. Edits run one at a time: a second press while one is on its way does
  // nothing, so no order is built from rows that the edit in flight is about to change (the ref is read at once; the state
  // only greys the buttons). A failed edit says so and leaves the rows as they were.
  const editing = useRef(false);
  const [editBusy, setEditBusy] = useState(false);
  const change = async (fn: () => Promise<void>) => {
    if (editing.current) return;
    editing.current = true;
    setEditBusy(true);
    try {
      try {
        await fn();
      } catch {
        message.error(t("files.playlists.failed"));
        return;
      }
      p.onChanged();
      await read();
    } finally {
      editing.current = false;
      setEditBusy(false);
    }
  };

  if (failed && detail === null) {
    // The first read failed: the error says so, and a retry reads the playlist again.
    return (
      <Alert
        type="error"
        showIcon
        message={t("files.playlists.loadFailed")}
        action={
          <Button size="small" className="pl-touch" onClick={() => void read()}>
            {t("files.playlists.retry")}
          </Button>
        }
      />
    );
  }
  if (detail === null) return null;

  const rows = detail.entries;
  const move = (i: number, delta: number) => {
    const j = i + delta;
    if (j < 0 || j >= rows.length || editing.current) return;
    const order = rows.map((r) => r.entry_id);
    [order[i], order[j]] = [order[j], order[i]];
    void change(() => reorderEntries(p.pls, order));
  };

  return (
    <div className="pl-editor" aria-label={t("files.playlists.editing", { name: p.name })} data-testid="playlist-editor">
      <div className="pl-editor-head">
        <strong>{t("files.playlists.editing", { name: p.name })}</strong>
        <Button size="small" className="pl-touch" aria-label={t("files.playlists.close")} icon={<X size={14} aria-hidden="true" />} onClick={p.onClose} />
      </div>
      {rows.length === 0 ? (
        <EmptyState size="compact" className="media-queue-empty" text={t("files.playlists.empty")} />
      ) : (
        <ol className="media-rows">
          {rows.map((r, i) => {
            const title = r.node && !r.unavailable ? displayTitle(r.node.name, getTags(r.node.id)) : null;
            return (
              <li key={r.entry_id} className="media-row" data-testid="playlist-entry">
                <span className="media-title">
                  {title === null ? (
                    <span className="media-title--off" data-testid="playlist-unavailable">
                      <bdi>{t("files.playlists.unavailable")}</bdi>
                    </span>
                  ) : (
                    <bdi dir="auto" title={title}>
                      {title}
                    </bdi>
                  )}
                </span>
                <span className="media-row-buttons">
                  <Button size="small" className="pl-touch" disabled={editBusy || i === 0} aria-label={t("files.playlists.moveUp", { name: title ?? t("files.playlists.unavailable") })}
                    icon={<ArrowUp size={14} aria-hidden="true" />} onClick={() => move(i, -1)} />
                  <Button size="small" className="pl-touch" disabled={editBusy || i === rows.length - 1} aria-label={t("files.playlists.moveDown", { name: title ?? t("files.playlists.unavailable") })}
                    icon={<ArrowDown size={14} aria-hidden="true" />} onClick={() => move(i, 1)} />
                  <Button size="small" danger className="pl-touch" aria-label={t("files.playlists.remove", { name: title ?? t("files.playlists.unavailable") })}
                    icon={<Trash2 size={14} aria-hidden="true" />} disabled={editBusy} onClick={() => void change(() => removeEntry(p.pls, r.entry_id))} />
                </span>
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}
