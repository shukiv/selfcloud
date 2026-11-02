// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Alert, ConfigProvider, Slider, theme } from "antd";
import { Maximize2, Pause, Play, SkipBack, SkipForward, X } from "lucide-react";
import HeadDownload from "./HeadDownload";
import IconButton from "../IconButton";
import { contentPath, getResume, listSubtitles, putResume } from "../../lib/files/api";
import { kindOf } from "../../lib/files/kind";
import { startDownload } from "../../lib/files/ops";
import { downloadAllowed } from "../../lib/files/roles";
import {
  currentSessionKey,
  getPlayback,
  closeViewer,
  showViewer,
  setCurrent,
  setMedia,
  setNav,
  stopPlayback,
  subscribePlayback,
  type PlaybackNav,
} from "../../lib/files/playback";
import { isPopupTarget, refusedPlay } from "../../lib/files/mediaKeys";
import { formatTime, clamp } from "../../lib/files/mediaKeys";
import { bindMediaSession, setPlaybackState } from "../../lib/files/mediaSession";
import { readTracks, type SubtitleTrack } from "../../lib/files/subtitles";
import type { Resume } from "../../lib/files/resume";
import type { Node } from "../../lib/files/types";
import { currentLang, useT } from "../../lib/i18n";
import { SIDEBAR } from "../../theme/tokens";
import { inlineLink } from "./Preview";
import MediaPlaylist from "./MediaPlaylist";
import TagCover, { useTagState } from "./TagCover";
import { displayArtist, displayTitle, hasCover, loadTags, seedTags, subscribeTags } from "../../lib/files/tags";
import { thumbnailPath } from "../../lib/files/thumbnail";

// MediaDock is the signed-in session drawn in the shell: the viewer (a full-window dialog with the playlist and its player)
// while it is open, and the mini player bar at the bottom of the shell once it is closed. One playlist and one player run
// for the whole session: closing the viewer only hides it, so audio goes on while the person browses. The player reads its
// place and saves it through the signed-in person's own routes, and reads its subtitles the same way. Share pages have
// their own playlist (PublicBrowse), with none of this.

// FOCUSABLE lists what Tab stops at inside the viewer.
const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

// fetchSessionLink asks for a link to a file of the playlist. A refused link is the error the playlist reads.
async function fetchSessionLink(n: Node) {
  const link = await inlineLink(n.id);
  if (link === null) throw new Error("unsafe link");
  return link;
}

// resumeSession reads the place the person stopped at in a file. A file with none, or a failure, has no place.
// A failed read is "unknown", not "none": nothing is saved over a place that may be stored (resume.ts, Resume).
async function resumeSession(n: Node): Promise<Resume> {
  try {
    const r = await getResume(n.id);
    return r.position_seconds > 0 ? { kind: "place", position: r.position_seconds, duration: r.duration_seconds } : { kind: "none" };
  } catch {
    return { kind: "unknown" };
  }
}

// saveSession keeps the place under the person the session started for. A session that has since changed person (or has
// ended: its player is still unmounting) writes nothing, so one person's place never goes to another's row. A failure is
// not shown: the place is kept again at the next pause or tick.
function saveSession(n: Node, position: number, duration: number, who: string): void {
  if (currentSessionKey() !== who) return;
  try {
    void putResume(n.id, position, duration).catch(() => undefined);
  } catch {
    // No session is left to keep a place for: nothing is saved.
  }
}

// subtitlesSession reads the subtitle files beside a video, each through its own content route with the person's session.
async function subtitlesSession(n: Node): Promise<SubtitleTrack[]> {
  const files = await listSubtitles(n.id);
  return readTracks(
    files,
    (id, range) => fetch(contentPath(id, "inline"), { credentials: "same-origin", cache: "no-store", headers: { Range: range } }),
    currentLang(),
  );
}

export default function MediaDock() {
  const snap = useSyncExternalStore(subscribePlayback, getPlayback, getPlayback);
  const { t } = useT();
  const [pos, setPos] = useState<{ position: number; total: string }>({ position: 0, total: "0" });
  const [refused, setRefused] = useState<string | null>(null);
  // toggleSlot is the header's place for the playlist button (set while the viewer is open, see the header).
  const [toggleSlot, setToggleSlot] = useState<HTMLElement | null>(null);
  // press says the browser refused to start a file for want of a press: the note shows in the mini player too.
  const [press, setPress] = useState(false);
  const node = snap.current?.node ?? null;
  const viewing = snap.viewerOpen;
  const canDownload = node ? downloadAllowed(node) : false;
  // The tags of the file on screen (decision 56): its title and artist show when they are read, and its name until then.
  const tags = useTagState(node?.id ?? "");
  const audio = node !== null && kindOf(node) === "audio";
  const coverSeen = useSyncExternalStore(subscribeTags, () => hasCover(node?.id ?? ""));
  useEffect(() => {
    if (!node || !audio) return;
    seedTags(node.id, node.tags);
    // A failed read keeps the name; playback does not wait for it.
    void loadTags([node.id]);
  }, [node?.id, audio]);

  // The session's system controls (the keys of a keyboard or headset, the lock screen) act on the file that plays.
  useEffect(() => {
    const media = snap.media;
    if (!media || node === null) return;
    const seekBy = (d: number) => {
      const end = Number.isFinite(media.duration) ? media.duration : Infinity;
      media.currentTime = clamp(media.currentTime + d, 0, end);
    };
    const unbind = bindMediaSession(
      {
        title: displayTitle(node.name, tags),
        artist: displayArtist(tags),
        album: tags?.state === "ready" ? tags.album : undefined,
        // The lock screen shows the cover once the file is known to have one: the listing says so, or the player has seen it.
        artwork: audio && (node.has_thumbnail === true || coverSeen) ? thumbnailPath(node.id, 256) : undefined,
      },
      {
      play: () => void media.play().catch((err: unknown) => refusedPlay(err) && setPress(true)),
      pause: () => media.pause(),
      previous: () => snap.nav?.previous(),
      next: () => snap.nav?.next(),
      seekBy,
      seekTo: (sec) => {
        media.currentTime = clamp(sec, 0, Number.isFinite(media.duration) ? media.duration : sec);
      },
    });
    const report = () => setPlaybackState(!media.paused && !media.ended, media.currentTime, Number.isFinite(media.duration) ? media.duration : 0);
    const events = ["play", "pause", "ended", "timeupdate", "durationchange", "loadedmetadata"];
    // A play that starts clears the note, whichever control started it.
    const started = () => setPress(false);
    for (const e of events) media.addEventListener(e, report);
    media.addEventListener("play", started);
    report();
    return () => {
      for (const e of events) media.removeEventListener(e, report);
      media.removeEventListener("play", started);
      unbind();
    };
    // The bindings read the element and the file; the navigation is read at the time of each control.
  }, [snap.media, node?.id, tags, audio, coverSeen]);

  const download = async () => {
    if (!node) return;
    const ok = await startDownload(node.id);
    if (!ok) setRefused(node.id);
  };

  const playlist = snap.seed ? (
    <MediaPlaylist
      start={snap.seed.start}
      folder={snap.seed.folder}
      selection={snap.seed.selection}
      saved
      fetchLink={fetchSessionLink}
      onCurrent={(n: Node | null) => setCurrent(n ? { node: n, kind: kindOf(n) === "video" ? "video" : "audio" } : null)}
      onPosition={setPos}
      resumeOf={resumeSession}
      saveOf={(n: Node, position: number, duration: number) => saveSession(n, position, duration, snap.key)}
      onPress={setPress}
      subtitlesOf={subtitlesSession}
      onElement={setMedia}
      onNav={(nav: PlaybackNav) => setNav(nav)}
      active={viewing}
      toggleSlot={toggleSlot}
    />
  ) : null;

  const viewerRef = useRef<HTMLDivElement>(null);
  const { token } = theme.useToken();
  // The viewer takes the focus on its container, not on Close, so a Space press reaches the player when it opens.
  useEffect(() => {
    if (viewing) viewerRef.current?.focus();
  }, [viewing]);
  // The page behind does not scroll while the viewer is open.
  useEffect(() => {
    if (!viewing) return;
    const was = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = was;
    };
  }, [viewing]);

  // viewerKey gives the open viewer what a dialog gives: Escape closes it, and Tab stays inside it. A combo box that is open
  // takes its own Escape.
  const viewerKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      if ((e.target as HTMLElement).getAttribute("aria-expanded") === "true") return;
      // A popup the page drew (a row's menu) takes its own Escape, through the portal this key bubbles through.
      if (isPopupTarget(e.target)) return;
      e.preventDefault();
      closeViewer();
      return;
    }
    if (e.key !== "Tab") return;
    const root = viewerRef.current;
    if (!root) return;
    // A closed drawer of the playlist is hidden, so its controls are not in the Tab order.
    const items = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => !el.hasAttribute("disabled") && el.closest(".media-drawer--closed") === null);
    if (items.length === 0) {
      e.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const at = document.activeElement;
    const outside = !root.contains(at);
    if (e.shiftKey && (outside || at === first || at === root)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && (outside || at === last)) {
      e.preventDefault();
      first.focus();
    }
  };

  return (
    <>
      {/* One element for the viewer, kept mounted while it is closed: the player inside it is the session, so closing the viewer
          must not stop the audio. Open, it is a full-window dialog. Closed, it is hidden and bears no dialog role, no aria-modal
          and no test id, so nothing reads it as an open viewer, and no focus is trapped in it. */}
      <div
        ref={viewerRef}
        {...(viewing
          ? { role: "dialog", "aria-modal": true, "aria-label": node?.name ?? "", "data-testid": "preview", tabIndex: -1, onKeyDown: viewerKey }
          : {})}
        style={
          viewing
            ? { position: "fixed", inset: 0, zIndex: 1000, display: "flex", flexDirection: "column", gap: 12, padding: 12, background: token.colorBgElevated }
            : { display: "none" }
        }
      >
        {viewing && (
          <>
            <header className="media-viewer-head" style={{ display: "flex", flexWrap: "nowrap", alignItems: "center", gap: 12 }}>
              {/* No autoFocus: the player's Space must reach the player when the viewer opens, not press Close. */}
              <IconButton label={t("files.preview.close")} onClick={() => closeViewer()} icon={<X size={18} aria-hidden="true" focusable="false" />} />
              {/* An audio file's cover, title and artist are on its stage (MediaArt), so the header keeps the buttons and the count. */}
              {audio ? (
                <span className="media-spacer" />
              ) : (
                // The name is one line that ends in an ellipsis; its full text is in the title.
                <p
                  className="media-head-name"
                  title={node ? displayTitle(node.name, tags) : ""}
                  style={{ flex: "1 1 0", margin: 0, fontWeight: 600, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                >
                  <bdi dir="auto">{node ? displayTitle(node.name, tags) : ""}</bdi>
                </p>
              )}
              {/* A playlist's count reads left to right in every language, as "2 / 5". */}
              <span aria-live="polite" dir="ltr" data-testid="media-count" style={{ fontVariantNumeric: "tabular-nums" }}>
                {pos.position} / {pos.total}
              </span>
              {/* The Playlist button is drawn into this slot by MediaPlaylist (toggleSlot), beside Download. */}
              <span ref={setToggleSlot} className="media-toggle-slot" style={{ display: "contents" }} />
              {canDownload && <HeadDownload label={t("files.act.download")} onClick={() => void download()} />}
            </header>
            {node && refused === node.id && <Alert type="info" role="status" showIcon message={t("files.err.downloadRestricted")} />}
          </>
        )}
        <div style={{ flex: "1 1 auto", minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>{playlist}</div>
      </div>
      {!viewing && (
        <MiniPlayer
          name={node?.name ?? ""}
          nod={node?.id ?? ""}
          audio={audio}
          media={snap.media}
          nav={snap.nav}
          press={press}
          onRefused={() => setPress(true)}
        />
      )}
    </>
  );
}

// MiniPlayer is the bar at the bottom of the shell while audio plays after the viewer closed. It drives the same element.
// Its controls read left to right in every language, as the viewer's bar does.
function MiniPlayer({
  name,
  nod,
  audio,
  media,
  nav,
  press,
  onRefused,
}: {
  name: string;
  nod: string;
  audio: boolean;
  media: HTMLMediaElement | null;
  nav: PlaybackNav | null;
  press: boolean;
  onRefused: () => void;
}) {
  const { t } = useT();
  const tags = useTagState(nod);
  const artist = displayArtist(tags);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const [dur, setDur] = useState(0);
  const [drag, setDrag] = useState<number | null>(null);
  useEffect(() => {
    if (!media) return;
    const sync = () => {
      setPlaying(!media.paused && !media.ended);
      setTime(media.currentTime);
      setDur(Number.isFinite(media.duration) ? media.duration : 0);
    };
    const events = ["play", "pause", "ended", "timeupdate", "durationchange", "loadedmetadata", "emptied", "seeked"];
    for (const e of events) media.addEventListener(e, sync);
    sync();
    return () => {
      for (const e of events) media.removeEventListener(e, sync);
    };
  }, [media]);

  // A play the browser refuses for want of a press is said here, since the viewer's bar is out of view.
  const toggle = () => {
    if (!media) return;
    if (media.paused) void media.play().catch((err: unknown) => refusedPlay(err) && onRefused());
    else media.pause();
  };
  const shown = drag ?? time;
  return (
    <div className="media-mini" role="region" aria-label={t("files.mini.label")} dir="ltr" data-testid="media-mini" style={{ background: SIDEBAR.bg, color: SIDEBAR.text }}>
      {audio && <TagCover nod={nod} kind="audio" size={40} enabled />}
      <p className="media-mini-name" title={name}>
        <bdi dir="auto">{displayTitle(name, tags)}</bdi>
        {artist !== undefined && (
          <span className="media-mini-artist" dir="auto">
            {artist}
          </span>
        )}
      </p>
      {press && (
        <p role="status" className="media-mini-press" data-testid="media-press">
          {t("files.media.pressPlay")}
        </p>
      )}
      {nav && (
        <IconButton className="media-mini-btn" label={t("files.media.previous")} onClick={() => nav.previous()} icon={<SkipBack size={18} aria-hidden="true" focusable="false" />} />
      )}
      {/* The play button stays filled and round: it is the bar's primary action. */}
      <IconButton
        className="media-mini-play"
        label={playing ? t("files.media.pause") : t("files.media.play")}
        onClick={toggle}
        disabled={!media}
        style={{ background: SIDEBAR.active, color: SIDEBAR.onActive }}
        icon={playing ? <Pause size={18} aria-hidden="true" focusable="false" /> : <Play size={18} aria-hidden="true" focusable="false" />}
      />
      {nav && <IconButton className="media-mini-btn" label={t("files.media.next")} onClick={() => nav.next()} icon={<SkipForward size={18} aria-hidden="true" focusable="false" />} />}
      <div className="media-mini-seek">
        <span className="media-mini-time" dir="ltr">
          {formatTime(shown)}
        </span>
        {/* antd reads a slider's direction from ConfigProvider, not from the DOM, so the bar's slider is wrapped in an ltr provider:
            left is the start in every language, as the time labels are. */}
        <ConfigProvider direction="ltr">
          <Slider
            min={0}
            max={dur || 0}
            step={1}
            value={shown}
            disabled={!media || dur === 0}
            tooltip={{ open: false }}
            ariaLabelForHandle={t("files.media.position")}
            ariaValueTextFormatterForHandle={(v: number) => formatTime(v)}
            onChange={(v) => setDrag(v)}
            onChangeComplete={(v) => {
              setDrag(null);
              if (media) media.currentTime = clamp(v, 0, dur);
            }}
          />
        </ConfigProvider>
        <span className="media-mini-time" dir="ltr">
          {formatTime(dur)}
        </span>
      </div>
      <IconButton className="media-mini-btn" label={t("files.mini.open")} onClick={() => showViewer()} icon={<Maximize2 size={18} aria-hidden="true" focusable="false" />} />
      <IconButton className="media-mini-btn" label={t("files.mini.close")} onClick={() => stopPlayback()} icon={<X size={18} aria-hidden="true" focusable="false" />} />
    </div>
  );
}
