// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Alert, Button } from "antd";
import IconButton from "../IconButton";
import { ListMusic, X } from "lucide-react";
import { ApiError } from "../../lib/apiError";
import { useT } from "../../lib/i18n";
import { kindOf } from "../../lib/files/kind";
import { Pager } from "../../lib/files/pager";
import { isPopupTarget } from "../../lib/files/mediaKeys";
import {
  addEnd,
  addNext,
  advance,
  appendPage,
  back,
  choose,
  cycleRepeat,
  failed,
  move,
  newQueue,
  played,
  remove,
  restricted,
  toggleShuffle,
  type Queue,
} from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";
import type { SubtitleTrack } from "../../lib/files/subtitles";
import type { Resume } from "../../lib/files/resume";
import MediaPlayer, { type Link, type MediaFailure } from "./MediaPlayer";
import MediaQueue from "./MediaQueue";
import MediaPlaylists from "./MediaPlaylists";
import { replaceEntries as replaceQueueEntries } from "../../lib/files/queue";
import { usePaletteOrLight } from "../../theme/theme";
import MediaArt from "./MediaArt";

// DRAWER_ID names the playlist drawer for the toggle's aria-controls.
const DRAWER_ID = "media-drawer";

// MediaPlaylist plays a list of video and audio files one after another, with the panel that shows the list. The list is
// the folder's playable files, read a page at a time as the player reaches their end (Pager), or the selection the person
// chose to play. The queue decides what plays (lib/files/queue.ts); this component starts the link of each file, reports
// the outcome back to the queue, and moves on. A file that fails is marked, its message stays on screen for SKIP_MS, and
// then the next file plays. A file the person may not download gets no link and is skipped at once.

// SKIP_MS is how long a failed file stays on screen, with its message, before the next file plays.
export const SKIP_MS = 2500;

// Status is the state of the link of the file on screen.
// restricted: the server refused the link because the person may not download the file; it is shown, then skipped.
type Status = "loading" | "ready" | "error" | "unsupported" | "restricted";

interface Current {
  id: string;
  status: Status;
  link?: Link;
  // resume is what is known of the place stopped at (resume.ts); tracks are the file's subtitle tracks, when read.
  resume?: Resume;
  tracks?: SubtitleTrack[];
}

export interface MediaPlaylistProps {
  // start is the file that plays first.
  start: Node;
  // folder reads the folder's playable files, a page at a time.
  folder: Pager;
  // selection, when given, is the list to play instead of the folder's files. The folder is then used only by the add
  // panel.
  selection?: Node[];
  // saved shows the saved playlists under the queue (decision 58). The signed-in player sets it; a share page does not,
  // since a share has no saved state.
  saved?: boolean;
  // fetchLink asks for a link to a file the person may play; it throws when the link is refused (ApiError
  // download_restricted, when the person may not download the file).
  fetchLink: (node: Node) => Promise<Link>;
  // onPosition is told the file on screen's place in the list, "2 / 3", so the viewer's header can show it.
  onPosition?: (pos: { position: number; total: string }) => void;
  // onCurrent is told which file is on screen, so the viewer can show its name.
  onCurrent?: (node: Node | null) => void;
  // onLost is asked when the link to a file is refused (401 or 404). It answers whether the share itself is gone, after
  // a check of the share: true stops the playlist; false makes the refusal that one file's failure, and the playlist moves on.
  onLost?: (err: unknown) => Promise<boolean>;
  // resumeOf reads where the person stopped in a file (Resume, resume.ts). It does not fail: a failed read is "unknown".
  // Only a signed-in session has one: a share page keeps no saved place.
  resumeOf?: (node: Node) => Promise<Resume>;
  // onPress is told whether the press-Play note shows for the file on screen.
  onPress?: (v: boolean) => void;
  // saveOf keeps the place in a file: its position and length, in seconds. Only a signed-in session keeps one.
  saveOf?: (node: Node, position: number, duration: number) => void;
  // subtitlesOf reads the subtitle tracks of a video; a failure gives none. Audio files are never asked.
  subtitlesOf?: (node: Node) => Promise<SubtitleTrack[]>;
  // onElement is given the player's element as it comes and goes, and onNav the playlist's Previous and Next, so the owner
  // of a session can play through them from outside the viewer.
  onElement?: (el: HTMLMediaElement | null) => void;
  onNav?: (nav: { next: () => void; previous: () => void }) => void;
  // active says the viewer shows the playlist, so the player reads its keys. A session kept for audio alone does not.
  active?: boolean;
  // toggleSlot is the element the Playlist button is drawn into, on the owner's head row beside Download (the viewer and
  // the share page each give one). Given as undefined, the button keeps a row of its own above the screen; given as null,
  // the slot is not on screen yet and no button is drawn.
  toggleSlot?: HTMLElement | null;
}

export default function MediaPlaylist({
  start,
  folder,
  selection,
  saved = false,
  fetchLink,
  onPosition,
  onCurrent,
  onLost,
  resumeOf,
  saveOf,
  subtitlesOf,
  onPress,
  onElement,
  onNav,
  active = true,
  toggleSlot,
}: MediaPlaylistProps) {
  const { t } = useT();
  const palette = usePaletteOrLight();
  // drawer says the playlist drawer is open. It is closed on every opening of the viewer (active turns on), and it is not
  // kept: closing the viewer resets it (decision 63).
  const [drawer, setDrawer] = useState(false);
  const toggleRef = useRef<HTMLButtonElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    if (!active) setDrawer(false);
  }, [active]);
  // Opening moves the focus into the drawer; closing by the person returns it to the toggle. A close the viewer makes (the
  // viewer is hidden) does not move the focus.
  // The focus moves in the task after the open: a press moves the focus to the toggle as it lands. A closed drawer is inert (out of
  // the focus order and the accessibility tree) and off the screen, not visibility: hidden, which Chrome keeps hidden for a moment
  // after the open, so the focus failed.
  useEffect(() => {
    if (!drawer) return;
    const id = window.setTimeout(() => closeRef.current?.focus(), 0);
    return () => window.clearTimeout(id);
  }, [drawer]);
  const closeDrawer = () => {
    setDrawer(false);
    toggleRef.current?.focus();
  };
  // Escape closes the drawer first, and the viewer only with the drawer closed. It is read in the capture phase so the viewer's
  // own Escape never sees it. A combo box or menu that is open, and a dialog (not the viewer), take their own Escape.
  useEffect(() => {
    if (!drawer) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      const at = e.target instanceof HTMLElement ? e.target : null;
      if (at !== null && at !== toggleRef.current && at.getAttribute("aria-expanded") === "true") return;
      // A popup the page drew (a row's menu, a confirm) takes its own Escape.
      if (isPopupTarget(at)) return;
      e.preventDefault();
      e.stopPropagation();
      setDrawer(false);
      toggleRef.current?.focus();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [drawer]);
  const lostRef = useRef(onLost);
  lostRef.current = onLost;
  const resumeRef = useRef(resumeOf);
  resumeRef.current = resumeOf;
  const saveRef = useRef(saveOf);
  saveRef.current = saveOf;
  const subsRef = useRef(subtitlesOf);
  subsRef.current = subtitlesOf;
  // playing says whether the file on screen plays now; the queue's current row shows it.
  const [playing, setPlaying] = useState(false);
  // fromSaved is set when a saved playlist replaces the queue. Such a queue ends at its own end: the folder it came from
  // is not read on, so the playlist never plays the folder's files after its own (slice x4).
  const [fromSaved, setFromSaved] = useState(false);
  const paged = selection === undefined && !fromSaved;
  const [q, setQ] = useState<Queue>(() => {
    // At mount no saved playlist has replaced the queue, so the list is the folder's when no selection was given.
    const base = selection === undefined ? folder.loaded : selection;
    const list = base.some((n) => n.id === start.id) ? base : [start, ...base];
    return newQueue(list, start.id, { shuffle: false, repeat: "off" });
  });
  // qRef is the queue as it is now. Steps that wait for a page read it, not the value the render had.
  const qRef = useRef(q);
  const [current, setCurrent] = useState<Current | null>(null);
  // stop says why the playlist has no file to play: the end of the list, or no file in it could play. Null while a file plays.
  // "lost" is a share that has gone (the share's own check said so); "unreadable" is a page of the folder that could not be
  // read, so the playlist cannot tell whether a file is still to come. The others are the queue's reasons (lib/files/queue.ts).
  const [stop, setStop] = useState<null | "end" | "failed" | "none" | "empty" | "lost" | "unreadable">(null);
  const [busy, setBusy] = useState(false);
  const [moreFailed, setMoreFailed] = useState(false);
  const [adding, setAdding] = useState(false);
  // skip is the pause before a failed file is passed over, and the file it is for. The timer acts only while that file is
  // still on screen: a choice the person makes meanwhile cancels it (cancelSkip).
  const skip = useRef<{ timer: ReturnType<typeof setTimeout>; id: string } | null>(null);
  // epoch counts the files the playlist starts. The player is keyed by it, so a file that plays again (the only file under
  // repeat all) starts from the beginning.
  const [epoch, setEpoch] = useState(0);
  const fetchRef = useRef(fetchLink);
  fetchRef.current = fetchLink;
  // ticket numbers the steps and picks. A step that is not the latest one does nothing once its wait ends: a pick or a
  // newer Next made meanwhile wins.
  const ticket = useRef(0);
  // alive is false once the viewer has closed, so no page is read for a playlist nobody sees.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      ticket.current++;
    };
  }, []);
  // shuffling is set while a shuffle is loading the folder for its round; a second press meanwhile does nothing.
  const shuffling = useRef(false);

  const commit = (next: Queue) => {
    qRef.current = next;
    setQ(next);
  };

  const cancelSkip = () => {
    if (skip.current !== null) clearTimeout(skip.current.timer);
    skip.current = null;
  };
  useEffect(() => cancelSkip, []);

  const currentId = q.current;
  const currentNode = currentId === null ? undefined : q.entries.find((e) => e.node.id === currentId)?.node;

  // loadMore reads the next page of the folder into the queue. It says whether the folder gave a page ("ok"), had no more
  // ("end"), or could not be read ("failed": the panel offers to try again, and the folder keeps its page).
  const loadMore = async (): Promise<"ok" | "end" | "failed"> => {
    if (!paged || qRef.current.capped) return "end";
    setBusy(true);
    try {
      const r = await folder.more();
      if (!alive.current) return "end";
      if (r.kind === "failed") {
        setMoreFailed(true);
        return "failed";
      }
      setMoreFailed(false);
      if (r.kind === "end") return "end";
      commit(appendPage(qRef.current, r.items).queue);
      return "ok";
    } finally {
      if (alive.current) setBusy(false);
    }
  };

  // step moves to the next file that can play, reading pages of the folder while the queue has nothing further loaded.
  // It ends when a file is chosen, the queue stops, or a page cannot be read. Each page read is one step of the loop, and
  // the folder is finite. A step superseded by a later pick or step does nothing when its wait ends.
  const step = async (from: Queue): Promise<void> => {
    const my = ++ticket.current;
    let cur = from;
    for (;;) {
      const a = advance(cur, paged && !cur.capped && folder.hasMore);
      if (a.kind === "play") {
        if (my !== ticket.current) return;
        commit(a.queue);
        setStop(null);
        setEpoch((e) => e + 1);
        return;
      }
      if (a.kind === "stop") {
        if (my !== ticket.current) return;
        commit(a.queue);
        setStop(a.reason);
        return;
      }
      const r = await loadMore();
      if (my !== ticket.current || !alive.current) return;
      if (r === "failed") {
        setStop("unreadable");
        return;
      }
      cur = qRef.current;
    }
  };

  // The link of the file on screen. A refused link marks the file restricted and moves on at once; any other failure
  // marks it failed and moves on after the pause.
  useEffect(() => {
    if (!currentNode) return;
    const id = currentNode.id;
    const failedAt = () => scheduleSkip(id);
    // A file the person may not download gets no request at all: the queue already knows it.
    const mark = qRef.current.entries.find((e) => e.node.id === id)?.mark;
    if (mark !== "ok") {
      void step(qRef.current);
      return;
    }
    let live = true;
    setCurrent({ id, status: "loading", resume: resumeRef.current ? { kind: "pending" } : { kind: "none" }, tracks: [] });
    setPlaying(false);
    // The link comes first and starts the file. The place and the subtitles come with it, not before it: a place that
    // arrives later applies while the file is in its first seconds (MediaPlayer), and the tracks attach when they come.
    // Neither read fails the file. Subtitles are asked for a video only.
    if (resumeRef.current) {
      resumeRef.current(currentNode).then(
        (r) => {
          if (live) setCurrent((c) => (c && c.id === id ? { ...c, resume: r } : c));
        },
        () => {
          if (live) setCurrent((c) => (c && c.id === id ? { ...c, resume: { kind: "unknown" } } : c));
        },
      );
    }
    if (subsRef.current && kindOf(currentNode) === "video") {
      subsRef.current(currentNode).then(
        (tracks) => {
          if (live) setCurrent((c) => (c && c.id === id ? { ...c, tracks } : c));
        },
        () => undefined,
      );
    }
    fetchRef.current(currentNode).then(
      (link) => {
        if (live) setCurrent((c) => (c && c.id === id ? { ...c, status: "ready", link } : c));
      },
      (err: unknown) => {
        if (!live) return;
        if (err instanceof ApiError && err.code === "download_restricted") {
          setCurrent({ id, status: "restricted" });
          commit(restricted(qRef.current, id));
          failedAt();
          return;
        }
        const fail = () => {
          setCurrent({ id, status: "error" });
          commit(failed(qRef.current, id));
          failedAt();
        };
        // A refused credential (401) or a link that is gone (404) may mean the share is gone. The share decides, after
        // its own check; a file's 404 alone is that file's failure.
        if (lostRef.current && err instanceof ApiError && (err.status === 401 || err.status === 404)) {
          void lostRef.current(err).then((lost) => {
            if (!live) return;
            if (lost) {
              cancelSkip();
              setStop("lost");
              return;
            }
            fail();
          });
          return;
        }
        fail();
      },
    );
    return () => {
      live = false;
    };
    // The link is asked for when the file on screen changes, or when the playlist starts a file again. The other values
    // are read through refs.
  }, [currentId, epoch]);

  useEffect(() => {
    onCurrent?.(currentNode ?? null);
    // Only the file on screen matters here.
  }, [currentId]);

  // scheduleSkip moves on to the next file after the pause that lets the message be read. It acts only if the file that
  // failed is still the one on screen.
  const scheduleSkip = (id: string) => {
    cancelSkip();
    const timer = setTimeout(() => {
      skip.current = null;
      if (qRef.current.current !== id) return;
      void step(qRef.current);
    }, SKIP_MS);
    skip.current = { timer, id };
  };

  // pick makes a file the person chose the one on screen: any pending skip is cancelled, the failure run starts again,
  // and a stop is cleared, so the player mounts for the file.
  const pick = (next: Queue) => {
    cancelSkip();
    ticket.current++;
    commit({ ...next, run: 0 });
    setStop(null);
    setEpoch((e) => e + 1);
  };

  // onFail is the player's report that the file could not play. A link the player could not renew (401 or 404) is asked
  // of onLost, the same as a refused link on the file's first request: the share may be gone, and then the page answers.
  const onFail = (f: MediaFailure) => {
    if (currentId === null) return;
    const id = currentId;
    if (f.kind === "error" && f.err instanceof ApiError && f.err.code === "download_restricted") {
      setCurrent({ id, status: "restricted" });
      commit(restricted(qRef.current, id));
      scheduleSkip(id);
      return;
    }
    const fail = () => {
      if (qRef.current.current !== id) return;
      setCurrent({ id, status: f.kind === "unsupported" ? "unsupported" : "error" });
      commit(failed(qRef.current, id));
      scheduleSkip(id);
    };
    if (f.kind === "error" && lostRef.current && f.err instanceof ApiError && (f.err.status === 401 || f.err.status === 404)) {
      void lostRef.current(f.err).then((lost) => {
        if (!alive.current || qRef.current.current !== id) return;
        if (lost) {
          cancelSkip();
          setStop("lost");
          return;
        }
        fail();
      });
      return;
    }
    fail();
  };

  // onPlaying: the file has started, so a run of failures starts again.
  const onPlaying = () => {
    if (currentId !== null) commit(played(qRef.current, currentId));
  };

  // next and previous are the person's choices: a pending skip is cancelled, and the failure run starts again.
  const next = () => {
    cancelSkip();
    void step({ ...qRef.current, run: 0 });
  };
  const previous = () => {
    const b = back(qRef.current);
    if (b) pick(b.queue);
  };

  // The owner of a session reaches Previous and Next through onNav; the functions read the current ones when they run.
  const stepFns = useRef({ next, previous });
  stepFns.current = { next, previous };
  useEffect(() => {
    onNav?.({ next: () => stepFns.current.next(), previous: () => stepFns.current.previous() });
    // Registered once: the functions above read the current step through the ref.
  }, []);

  const kind = currentNode ? kindOf(currentNode) : "file";
  const playable = kind === "video" || kind === "audio";
  const shownStatus = current && currentNode && current.id === currentNode.id ? current.status : "loading";
  const position = currentId === null ? 0 : q.entries.findIndex((e) => e.node.id === currentId) + 1;
  const total = `${q.entries.length}${paged && folder.hasMore ? "+" : ""}`;
  useEffect(() => {
    onPosition?.({ position, total });
    // Only the place in the list matters here.
  }, [position, total]);

  // The panel's actions. Each one is a change of the queue; the file on screen changes only when the queue says so.
  const removeEntry = (id: string) => {
    const was = qRef.current.current === id;
    commit(remove(qRef.current, id));
    if (was) {
      cancelSkip();
      void step({ ...remove(qRef.current, id), run: 0 });
    }
  };

  // shuffleToggle turns shuffle on or off. Turning it on reads the folder up to the queue's cap first, so the round covers
  // every file the queue can hold, not only the pages loaded so far. The reading stops at the cap or at the end of the
  // folder (or when a page cannot be read), so it is bounded. No link is asked for by reading a page.
  // A press while the folder is still being read does nothing: the round is already being dealt, and a second toggle would
  // turn it off again. The reading stops when the viewer closes, and when a page cannot be read (the round is then dealt
  // from the files loaded).
  const shuffleToggle = async () => {
    if (qRef.current.shuffle) {
      commit(toggleShuffle(qRef.current));
      return;
    }
    if (shuffling.current) return;
    shuffling.current = true;
    try {
      while (alive.current && paged && !qRef.current.capped && folder.hasMore) {
        if ((await loadMore()) === "failed") break;
      }
      if (alive.current) commit(toggleShuffle(qRef.current));
    } finally {
      shuffling.current = false;
    }
  };

  // The add panel offers the folder's files that are not in the queue, in a selection too: the folder is what it adds from.
  const candidates = folder.loaded.filter((n) => !q.entries.some((e) => e.node.id === n.id));

  // The Playlist button is the playlist icon at every width; its name is its label and its tooltip (decision 68).
  const toggle = (
    <IconButton
      ref={toggleRef}
      className="media-btn media-playlist-toggle"
      label={t("files.media.queue")}
      aria-expanded={drawer}
      aria-controls={DRAWER_ID}
      onClick={() => (drawer ? closeDrawer() : setDrawer(true))}
      icon={<ListMusic size={18} aria-hidden="true" focusable="false" />}
    />
  );

  return (
    <div className="media-playlist" data-testid="media-playlist">
      <div className="media-main">
        {toggleSlot === undefined ? <div className="media-toolbar">{toggle}</div> : toggleSlot ? createPortal(toggle, toggleSlot) : null}
        <div className={`media-screen${drawer ? " media-screen--drawer" : ""}`} data-testid="media-screen">
          {stop === "end" ? (
            <p role="status">{t("files.media.ended")}</p>
          ) : stop === "lost" ? (
            <Alert type="warning" role="alert" showIcon message={t("files.media.lost")} />
          ) : stop === "failed" ? (
            <Alert
              type="info"
              role="status"
              showIcon
              message={t("files.media.stoppedFailures")}
              action={
                <Button size="small" onClick={next}>
                  {t("files.media.carryOn")}
                </Button>
              }
            />
          ) : stop === "none" ? (
            <Alert type="info" role="status" showIcon message={t("files.media.stoppedNone")} />
          ) : stop === "empty" ? (
            <Alert type="info" role="status" showIcon message={t("files.media.stoppedEmpty")} />
          ) : stop === "unreadable" ? (
            <Alert
              type="warning"
              role="alert"
              showIcon
              message={t("files.media.stoppedUnreadable")}
              action={
                <Button size="small" onClick={() => void step({ ...qRef.current, run: 0 })}>
                  {t("files.media.retry")}
                </Button>
              }
            />
          ) : currentNode === undefined ? (
            <p role="status">{t("files.media.ended")}</p>
          ) : shownStatus === "loading" ? (
            <p role="status">{t("files.preview.loading")}</p>
          ) : shownStatus === "restricted" ? (
            <Alert
              type="info"
              role="alert"
              showIcon
              message={t("files.err.downloadRestricted")}
              action={
                <Button size="small" onClick={next}>
                  {t("files.media.next")}
                </Button>
              }
            />
          ) : shownStatus === "error" || shownStatus === "unsupported" ? (
            <div>
              <Alert
                type="warning"
                role="alert"
                showIcon
                message={t("files.media.skipped")}
                description={shownStatus === "unsupported" ? t("files.preview.unsupported") : undefined}
                action={
                  <Button size="small" onClick={next}>
                    {t("files.media.next")}
                  </Button>
                }
              />
            </div>
          ) : current?.link && playable ? (
            <MediaPlayer
              key={`${currentNode.id}:${epoch}`}
              kind={kind === "video" ? "video" : "audio"}
              name={currentNode.name}
              link={current.link}
              mint={() => fetchRef.current(currentNode)}
              onFail={onFail}
              onEnded={next}
              onPlaying={onPlaying}
              onPlayState={setPlaying}
              loop={q.repeat === "one"}
              // Every file the playlist starts plays on load, the first one the person opened included. The browser may
              // refuse that for want of a press; the player then shows the note.
              autoPlay
              resume={current.resume}
              tracks={current.tracks}
              onPress={onPress}
              onSave={saveRef.current ? (pos, dur) => saveRef.current?.(currentNode, pos, dur) : undefined}
              onElement={onElement}
              active={active}
              nav={{ next, previous }}
              art={kind === "audio" ? <MediaArt node={currentNode} /> : undefined}
            />
          ) : null}
          {/* A stopped audio list keeps the last track's cover and name under its message, as the header did before (decision 63). */}
          {stop !== null && kind === "audio" && currentNode !== undefined && <MediaArt node={currentNode} />}
        <div
          id={DRAWER_ID}
          className={`media-drawer${drawer ? "" : " media-drawer--closed"}`}
          data-media-drawer=""
          data-open={drawer ? "true" : "false"}
          {...(drawer ? {} : { inert: "" })}
          style={{ background: palette.card, color: palette.text, borderColor: palette.line }}
        >
          <div className="media-drawer-head">
            <Button
              ref={closeRef}
              className="media-btn media-drawer-close"
              aria-label={t("files.media.closePlaylist")}
              onClick={closeDrawer}
              icon={<X size={18} aria-hidden="true" />}
            />
          </div>
          <MediaQueue
            entries={q.entries}
            currentId={currentId}
            repeat={q.repeat}
            shuffle={q.shuffle}
            capped={q.capped || (paged && folder.cut)}
            moreLeft={paged && folder.hasMore && !q.capped && !folder.cut}
            busy={busy}
            candidates={candidates}
            adding={adding}
            playing={playing}
            onAddOpen={() => setAdding((v) => !v)}
            onAdd={(n, where) => commit(where === "next" ? addNext(qRef.current, n) : addEnd(qRef.current, n))}
            onLoadMore={() => void loadMore()}
            onPlay={(id) => {
              const entry = qRef.current.entries.find((e) => e.node.id === id);
              if (entry && entry.mark !== "restricted" && entry.mark !== "unavailable") pick(choose(qRef.current, id));
            }}
            onRemove={removeEntry}
            onMove={(id, delta) => commit(move(qRef.current, id, delta))}
            onShuffle={() => void shuffleToggle()}
            onRepeat={() => commit(cycleRepeat(qRef.current))}
            saved={
              saved ? (
                <MediaPlaylists
                  queue={q.entries}
                  onOpen={(entries) => {
                    // Opening a saved playlist plays it: its first playable file starts. The file that was on screen is not in
                    // the list, so it would otherwise leave the player with no file to show.
                    setFromSaved(true);
                    const next = replaceQueueEntries(qRef.current, entries);
                    const first = next.entries.find((e) => e.mark === "ok");
                    if (first) pick(choose(next, first.node.id));
                    else commit(next);
                  }}
                />
              ) : undefined
            }
          />
        </div>
        </div>

        {/* One message for an unreadable folder: when the playlist has stopped as unreadable, its alert above says it. */}
        {moreFailed && stop !== "unreadable" && (
          <Alert
            type="warning"
            role="alert"
            showIcon
            message={t("files.media.stoppedUnreadable")}
            action={
              <Button size="small" onClick={() => void loadMore()}>
                {t("files.media.retry")}
              </Button>
            }
            style={{ marginTop: 8 }}
          />
        )}
      </div>

    </div>
  );
}
