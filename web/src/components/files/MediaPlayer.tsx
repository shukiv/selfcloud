// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Gate, NO_RENEWALS, PROGRESS_SECONDS, afterProgress, mediaVerdict, takeRenewal, type Renewals } from "../../lib/files/media";
import { clamp, refusedPlay } from "../../lib/files/mediaKeys";
import { LATE_START_S, SaveClock, startAt as placeToStart, type Resume } from "../../lib/files/resume";
import { MAX_SUBTITLE_BYTES, decodeSubtitle, parseCues, placeCue, type Cue, type SubtitleTrack } from "../../lib/files/subtitles";
import { currentLang, useT } from "../../lib/i18n";
import { readPref, writePref } from "../../lib/prefs";
import { session } from "../../lib/session";
import { useStoreSelect } from "../../lib/store";
import MediaControls, { type Nav } from "./MediaControls";

// MediaPlayer plays one video or audio file from a signed link. The link ends at a fixed time, and the server refuses
// every request for it after that, so a seek or a resume past the end fails. Then the player asks for a new link and
// goes on at the same place, playing or paused as it was. Its decisions read no clock (lib/files/media.ts): a failure
// is renewed once if playback had not moved on, and a stall is renewed a few times at most. Only a media error reaches
// onFail; a stall never does, and the player simply keeps buffering when it has no links left to ask for.
// The owner of the player keys it by file, so moving to another file unmounts this one, which pauses it.
// The bar under the picture is MediaControls. The element has no controls of its own.

export interface Link {
  url: string;
}

export type MediaFailure = { kind: "unsupported" } | { kind: "error"; err: unknown };

// A subtitle track (SubtitleTrack) is added to the element as a text track, so the browser draws its cues; no address is
// given to the element. A file loaded from this device is read in the browser only and lasts for this viewing.

// SubtitleSize is the cue text's size: small, medium (the browser's own) and large, a class on the video (index.css).
export type SubtitleSize = "s" | "m" | "l";

// OFFSET_LIMIT bounds the timing offset either way, in seconds; the offset steps and rests on tenths.
const OFFSET_LIMIT = 60;

// cueOriginals keeps each cue's own start and end, so an offset shifts from the file's times, never from the last shift.
const cueOriginals = new WeakMap<TextTrackCue, { start: number; end: number }>();

function clampOffset(v: number): number {
  return Math.round(clamp(v, -OFFSET_LIMIT, OFFSET_LIMIT) * 10) / 10;
}

// readOffset reads the offset kept for a video file in this browser. Without a key, or with a value that does not read,
// the offset is 0.
function readOffset(nodeId?: string): number {
  if (!nodeId) return 0;
  const raw = readPref(`selfcloud.subtitleOffset.${nodeId}`);
  const v = raw === null ? NaN : Number(raw);
  return Number.isFinite(v) ? clampOffset(v) : 0;
}

// readSize reads the person's cue size; anyone else's key is never read. Medium is the default.
function readSize(userId: string | null): SubtitleSize {
  const v = readPref(`selfcloud.subtitleSize.${userId ?? "visitor"}`);
  return v === "s" || v === "l" ? v : "m";
}

// shiftCue moves one cue to its own time plus the offset, never below 0, on the millisecond the formats keep.
function shiftCue(cue: TextTrackCue, off: number): void {
  const orig = cueOriginals.get(cue);
  if (!orig) return;
  cue.startTime = Math.max(0, Math.round((orig.start + off) * 1000) / 1000);
  cue.endTime = Math.max(0, Math.round((orig.end + off) * 1000) / 1000);
}

function shiftTrack(tt: TextTrack, off: number): void {
  const cues = tt.cues;
  if (!cues) return;
  for (let i = 0; i < cues.length; i++) shiftCue(cues[i], off);
}

interface Props {
  kind: "video" | "audio";
  name: string;
  link: Link;
  // mint asks for a new link to the same file. It may throw; the player reports the throw as an error.
  mint: () => Promise<Link>;
  onFail: (f: MediaFailure) => void;
  // nav adds Previous and Next to the bar, when the player is in a playlist.
  nav?: Nav;
  // loop repeats the file by the element's own loop, so the repeat needs no request and no new file.
  loop?: boolean;
  // onEnded and onPlaying tell the playlist when the file ends and when it has started to play.
  onEnded?: () => void;
  onPlaying?: () => void;
  // autoPlay starts the file once it has loaded. The browser may refuse, for want of a press: the note shows then.
  autoPlay?: boolean;
  // resume is what is known of the place the person stopped at (resume.ts). A place applies once it is known and the person
  // has not moved the playhead: at the first load, or later while the file is in its first seconds. A renewed link resumes
  // at its own place.
  resume?: Resume;
  // onPress tells the owner whether the press-Play note shows, so a bar that is out of view can show it too.
  onPress?: (v: boolean) => void;
  // onSave is told a place to keep: the position and the length, in seconds (0 when unknown). The player decides when:
  // at most every 15 seconds while the file plays, on pause, and at the end (position 0). Not given, nothing is saved.
  onSave?: (position: number, duration: number) => void;
  // onPlayState tells the owner whether the file plays now: true on play, false on pause and at the end.
  onPlayState?: (playing: boolean) => void;
  // onElement is given the element once it exists, and null when the player goes; the owner of a session plays through it.
  onElement?: (el: HTMLMediaElement | null) => void;
  // tracks are the subtitle files of a video, read already. The bar offers them; none is shown until the person picks one.
  tracks?: SubtitleTrack[];
  // subtitleKey is the video file's node id: the timing offset is kept under it in this browser, so the same file opens
  // with the offset it was left at. Without it the offset lasts for this viewing only.
  subtitleKey?: string;
  // active says the viewer shows the player, so its keys are read. A player kept on the page for audio alone is not active.
  active?: boolean;
  // keysInBox says the player sits in the session's box on the Media page, so a key aimed outside the box is the page's.
  keysInBox?: boolean;
  // art is the audio file's stage above its bar: the cover, the name and the tags (MediaArt). Nothing for a video.
  art?: ReactNode;
}

// STALL_MS is how long a player may wait for data while playing before it asks for a new link. Some browsers report a
// refused range as a stall and no error at all.
const STALL_MS = 4000;

export default function MediaPlayer({
  kind,
  name,
  link,
  mint,
  onFail,
  nav,
  loop = false,
  onEnded,
  onPlaying,
  autoPlay = false,
  resume: placeKnown = { kind: "none" },
  onPress,
  onSave,
  onPlayState,
  onElement,
  tracks,
  subtitleKey,
  active = true,
  keysInBox = false,
  art,
}: Props) {
  const { t } = useT();
  const el = useRef<HTMLMediaElement | null>(null);
  // mediaEl and stageEl are the same elements as state, so the bar renders once they exist.
  const [mediaEl, setMediaEl] = useState<HTMLMediaElement | null>(null);
  const [stageEl, setStageEl] = useState<HTMLDivElement | null>(null);
  // needPress is set when the browser refused to play for want of a gesture: the bar says so until the file plays.
  const [needPress, setNeedPress] = useState(false);
  const [current, setCurrent] = useState<Link>(link);
  const renewals = useRef<Renewals>(NO_RENEWALS);
  const gate = useRef(new Gate());
  // resume is where playback goes back to once a renewed link has loaded, and whether it was playing.
  const resume = useRef<{ at: number; play: boolean } | null>(null);
  // base is the playback place the current link began at, and renewed says whether the current link was asked for
  // after a failure. Together with the position they tell a failure before progress from one after it.
  const base = useRef(0);
  const renewed = useRef(false);
  // pending is set while a new link is being asked for: a failure reported meanwhile is the same one, not a new one.
  const pending = useRef(false);
  const stall = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The callbacks are read through refs, so a render with new ones does not restart a request in flight.
  const mintRef = useRef(mint);
  mintRef.current = mint;
  const failRef = useRef(onFail);
  failRef.current = onFail;
  const currentRef = useRef(current);
  currentRef.current = current;
  const endedRef = useRef(onEnded);
  endedRef.current = onEnded;
  const playingRef = useRef(onPlaying);
  playingRef.current = onPlaying;
  const saveRef = useRef(onSave);
  saveRef.current = onSave;
  const playStateRef = useRef(onPlayState);
  playStateRef.current = onPlayState;
  const elementRef = useRef(onElement);
  elementRef.current = onElement;
  // clock decides when a place is kept; started says the first load has happened (the start place and the play apply once).
  const clock = useRef(new SaveClock());
  const started = useRef(false);
  const resumeRef = useRef<Resume>(placeKnown);
  resumeRef.current = placeKnown;
  // moved says the person moved the playhead (a seek that was not the player's own). ownSeek marks the player's next seek.
  // placed says the place was decided for this file: it applies once.
  const moved = useRef(false);
  const ownSeek = useRef(false);
  const placed = useRef(false);
  const pressRef = useRef(onPress);
  pressRef.current = onPress;
  const autoRef = useRef(autoPlay);
  autoRef.current = autoPlay;
  // subs are the subtitle tracks the element has, and which one is shown (-1 for none).
  const textTracks = useRef<TextTrack[]>([]);
  const [subs, setSubs] = useState<{ labels: string[]; active: number }>({ labels: [], active: -1 });
  // loaded are the tracks read from this device for this viewing: they are never sent to the server, and they go when
  // the player does.
  const loaded = useRef<{ label: string; tt: TextTrack }[]>([]);
  // offset is the timing shift of every cue of this video, and size the person's cue size. subsNote says a device file
  // held no cues.
  const [offset, setOffset] = useState(0);
  const offsetRef = useRef(0);
  const userId = useStoreSelect(session, (s) => s?.user.id ?? null);
  const [size, setSize] = useState<SubtitleSize>("m");
  const [subsNote, setSubsNote] = useState(false);

  // The offset is the file's own, read once for it; a player handed another file's key reads that one's.
  useEffect(() => {
    const off = readOffset(subtitleKey);
    offsetRef.current = off;
    setOffset(off);
    for (const tt of textTracks.current) shiftTrack(tt, off);
  }, [subtitleKey]);

  // The size is the signed-in person's own; a visitor has a key of their own, and one person's choice never reads for
  // another.
  useEffect(() => {
    setSize(readSize(userId));
  }, [userId]);

  const clearStall = () => {
    if (stall.current !== null) clearTimeout(stall.current);
    stall.current = null;
  };

  // Leaving the page or moving to another file: no request is still wanted, and nothing keeps playing. This runs before
  // the element leaves the page, so the element is still there to pause.
  useLayoutEffect(() => {
    const g = gate.current;
    return () => {
      g.cancel();
      clearStall();
      const media = el.current;
      // The place is kept before the pause, so closing a file that plays keeps where it was.
      if (media && canSave() && !media.ended && clock.current.pause(performance.now(), media.currentTime)) {
        saveNow(media);
      }
      media?.pause();
      pressRef.current?.(false);
    };
  }, []);

  // seekOwn moves the playhead for the player itself (a place, a renewed link): it is not the person's move.
  const seekOwn = (media: HTMLMediaElement, to: number) => {
    if (Math.abs(media.currentTime - to) < 0.5) return;
    ownSeek.current = true;
    media.currentTime = to;
  };
  // canSave says a place may be kept now: a save is wanted, and the place is known, or the read failed and the person has
  // moved the playhead since (see Resume in resume.ts).
  const canSave = () => {
    if (!saveRef.current) return false;
    const r = resumeRef.current;
    return r.kind === "none" || r.kind === "place" || (r.kind === "unknown" && moved.current);
  };
  // placeFrom applies the place once it is known, if the person has not moved the playhead and the file is still in its first
  // seconds. Before the first load it waits for that load.
  const placeFrom = (media: HTMLMediaElement) => {
    if (placed.current || media.readyState < 1) return;
    const r = resumeRef.current;
    if (r.kind === "pending") return;
    placed.current = true;
    if (r.kind !== "place" || moved.current || media.currentTime >= LATE_START_S) return;
    const dur = Number.isFinite(media.duration) ? media.duration : 0;
    const at = placeToStart({ position: r.position, duration: r.duration }, dur);
    if (at > 0) seekOwn(media, at);
  };

  // saveNow passes the element's place to the owner, with its length (0 when the browser does not know it).
  const saveNow = (media: HTMLMediaElement) => {
    const dur = Number.isFinite(media.duration) ? media.duration : 0;
    saveRef.current?.(media.currentTime, dur);
  };

  // renew asks for a new link and resumes at the place playback had reached. A failure reports itself when no link is
  // left to ask for or the ask fails; a stall does not, since the player keeps buffering.
  const renew = async (media: HTMLMediaElement, after: "error" | "stall") => {
    if (pending.current) return;
    const at = media.currentTime;
    const next = takeRenewal(afterProgress(renewals.current, at), at);
    if (!next.allowed) {
      if (after === "error") failRef.current({ kind: "error", err: null });
      return;
    }
    renewals.current = next.next;
    resume.current = { at, play: !media.paused };
    pending.current = true;
    const id = gate.current.begin();
    try {
      const fresh = await mintRef.current();
      if (!gate.current.current(id)) return;
      pending.current = false;
      if (fresh.url === currentRef.current.url) {
        // The same address as the link that failed: the browser keeps the element's error and loads nothing, so no
        // event comes for this link. It failed the same way, and is treated as the fresh link that did.
        resume.current = null;
        if (after === "error") {
          const progressed = at > base.current + PROGRESS_SECONDS;
          const v = mediaVerdict(media.error?.code ?? null, progressed, true, false);
          failRef.current(v === "unsupported" ? { kind: "unsupported" } : { kind: "error", err: null });
        }
        return;
      }
      base.current = at;
      renewed.current = true;
      setCurrent(fresh);
    } catch (err) {
      if (!gate.current.current(id)) return;
      pending.current = false;
      resume.current = null;
      if (after === "error") failRef.current({ kind: "error", err });
    }
  };

  const onError = () => {
    const media = el.current;
    if (!media || pending.current) return;
    clearStall();
    const at = media.currentTime;
    const progressed = at > base.current + PROGRESS_SECONDS;
    const mayRenew = takeRenewal(afterProgress(renewals.current, at), at).allowed;
    const verdict = mediaVerdict(media.error?.code ?? null, progressed, renewed.current, mayRenew);
    if (verdict === "renew") void renew(media, "error");
    else if (verdict === "unsupported") failRef.current({ kind: "unsupported" });
    else failRef.current({ kind: "error", err: null });
  };

  // playNow starts the file. A refusal for want of a press shows the note; any other refusal is the element's own error.
  const playNow = (media: HTMLMediaElement) => {
    Promise.resolve(media.play()).catch((err: unknown) => refusedNote(err));
  };

  const onLoaded = () => {
    const media = el.current;
    if (!media) return;
    const r = resume.current;
    if (r) {
      resume.current = null;
      seekOwn(media, r.at);
      if (r.play) playNow(media);
      return;
    }
    // The first load: the place applies if it is known, and the file starts when autoPlay asks for it.
    if (started.current) return;
    started.current = true;
    placeFrom(media);
    if (autoRef.current) playNow(media);
  };

  // A place that arrives after the first load applies now, as placeFrom decides.
  useEffect(() => {
    const media = el.current;
    if (media && started.current) placeFrom(media);
    // placeFrom reads only refs; the resume value is the dependency.
  }, [placeKnown]);

  // onSeeking: a seek that is not the player's own is the person's move.
  const onSeeking = () => {
    if (ownSeek.current) {
      ownSeek.current = false;
      return;
    }
    moved.current = true;
  };

  const onWaiting = () => {
    clearStall();
    stall.current = setTimeout(() => {
      stall.current = null;
      const media = el.current;
      if (!media || media.paused) return;
      void renew(media, "stall");
    }, STALL_MS);
  };

  const onTime = () => {
    const media = el.current;
    if (!media) return;
    renewals.current = afterProgress(renewals.current, media.currentTime);
    if (canSave() && !media.paused && !media.ended && clock.current.tick(performance.now(), media.currentTime)) saveNow(media);
  };

  const onPlay = () => {
    clearStall();
    setNeedPress(false);
    playStateRef.current?.(true);
    playingRef.current?.();
  };

  const onPause = () => {
    playStateRef.current?.(false);
    const media = el.current;
    if (media && canSave() && !media.ended && clock.current.pause(performance.now(), media.currentTime)) saveNow(media);
  };

  const onEnd = () => {
    playStateRef.current?.(false);
    // A file that ends keeps position 0, so its next play starts at the beginning.
    clock.current.ended(performance.now());
    const media = el.current;
    if (media && canSave()) saveRef.current?.(0, Number.isFinite(media.duration) ? media.duration : 0);
    endedRef.current?.();
  };
  // refusedNote shows the press-Play note when the browser refused to play for want of a gesture. Any other rejection is
  // left as it was (the element's own error events report a file that cannot play).
  const refusedNote = (err: unknown) => {
    if (refusedPlay(err)) setNeedPress(true);
  };
  // The owner hears the note too, so a bar that is out of view can show it. It is cleared with the note.
  useEffect(() => {
    pressRef.current?.(needPress);
  }, [needPress]);

  const attach = (node: HTMLMediaElement | null) => {
    el.current = node;
    setMediaEl(node);
  };
  useEffect(() => {
    elementRef.current?.(mediaEl);
    return () => elementRef.current?.(null);
  }, [mediaEl]);

  // addCues turns parsed cues into the element's own, placed above the bar and shifted by the offset in force. A cue the
  // browser will not take is left out; the others show.
  const addCues = (tt: TextTrack, cues: Cue[]) => {
    const coarse = window.matchMedia?.("(pointer: coarse)").matches ?? false;
    for (const c of cues) {
      try {
        const cue = new window.VTTCue(c.start, c.end, c.text);
        placeCue(cue, coarse);
        cueOriginals.set(cue, { start: c.start, end: c.end });
        shiftCue(cue, offsetRef.current);
        tt.addCue(cue);
      } catch {
        // A cue the browser will not take is left out; the others show.
      }
    }
  };

  // The subtitle tracks are added to the element once it exists. The browser draws their cues; none is shown until picked.
  // Beside files come first, then the files loaded from the device for this viewing.
  useEffect(() => {
    const media = mediaEl;
    if (!media || kind !== "video" || !tracks || tracks.length === 0 || typeof window.VTTCue === "undefined") return;
    const made: TextTrack[] = [];
    for (const track of tracks) {
      const tt = media.addTextTrack("subtitles", track.label);
      addCues(tt, track.cues);
      tt.mode = "disabled";
      made.push(tt);
    }
    textTracks.current = [...made, ...loaded.current.map((l) => l.tt)];
    for (const tt of textTracks.current) tt.mode = "disabled";
    setSubs({ labels: [...tracks.map((track) => track.label), ...loaded.current.map((l) => l.label)], active: -1 });
    return () => {
      for (const tt of made) tt.mode = "disabled";
      textTracks.current = [];
    };
  }, [mediaEl, tracks, kind]);

  // pickTrack shows the chosen subtitle track, or none for -1.
  const pickTrack = (i: number) => {
    textTracks.current.forEach((tt, j) => {
      tt.mode = j === i ? "showing" : "disabled";
    });
    setSubs((s) => ({ ...s, active: i }));
  };

  // setOffsetTo shifts every cue of every track of this video to the offset, clamped and on tenths, and keeps it for the
  // file when its key is known. A storage that refuses keeps the choice for this page.
  const setOffsetTo = (v: number) => {
    const off = clampOffset(v);
    offsetRef.current = off;
    setOffset(off);
    for (const tt of textTracks.current) shiftTrack(tt, off);
    if (subtitleKey) writePref(`selfcloud.subtitleOffset.${subtitleKey}`, String(off));
  };

  // pickSize applies the person's cue size and keeps it under their own key.
  const pickSize = (s: SubtitleSize) => {
    setSize(s);
    writePref(`selfcloud.subtitleSize.${userId ?? "visitor"}`, s);
  };

  // loadFile reads a subtitle file from this device, in the browser only: no request of any kind carries the file or its
  // text. Only its first MAX_SUBTITLE_BYTES are read. With cues it becomes a track named after the file and shown at
  // once; with none, the note under the bar says so and nothing is added. It lasts while this video is open.
  const loadFile = async (file: File) => {
    const media = el.current;
    if (!media || kind !== "video" || typeof window.VTTCue === "undefined") return;
    setSubsNote(false);
    let cues: Cue[] = [];
    try {
      const bytes = new Uint8Array(await file.slice(0, MAX_SUBTITLE_BYTES).arrayBuffer());
      cues = parseCues(decodeSubtitle(bytes, currentLang(), file.size > MAX_SUBTITLE_BYTES));
    } catch {
      // A file that cannot be read gives no track; the note says none were found.
    }
    if (cues.length === 0) {
      setSubsNote(true);
      return;
    }
    const tt = media.addTextTrack("subtitles", file.name);
    addCues(tt, cues);
    tt.mode = "disabled";
    loaded.current.push({ label: file.name, tt });
    textTracks.current.push(tt);
    const i = textTracks.current.length - 1;
    setSubs((s) => ({ labels: [...s.labels, file.name], active: i }));
    pickTrack(i);
  };

  const handlers = {
    onError,
    onLoadedMetadata: onLoaded,
    onWaiting,
    onPlay,
    // A resumed file (after a wait) reports playing too; the state is the same as on play.
    onPlaying: onPlay,
    onPause,
    onSeeking,
    onCanPlay: clearStall,
    onEmptied: clearStall,
    onTimeUpdate: onTime,
    onEnded: onEnd,
  };
  // A timeline reads left to right in every language, so the controls are not mirrored.
  return (
    <div ref={setStageEl} className="media-stage" data-kind={kind} dir="ltr" data-testid="media-stage">
      {kind === "video" ? (
        // playsInline keeps the picture in its place on iPhone, where a video would otherwise open the system player.
        <video ref={attach} src={current.url} aria-label={name} dir="ltr" loop={loop} playsInline className={`media-picture media-cue-${size}`} {...handlers} />
      ) : (
        <audio ref={attach} src={current.url} aria-label={name} dir="ltr" loop={loop} className="media-sound" {...handlers} />
      )}
      {kind === "audio" && art}
      <MediaControls
        media={mediaEl}
        kind={kind}
        stage={stageEl}
        nav={nav}
        needPress={needPress}
        onNeedPress={setNeedPress}
        subtitles={
          kind === "video"
            ? { ...subs, onPick: pickTrack, onLoad: (f: File) => void loadFile(f), offset, onOffset: setOffsetTo, size, onSize: pickSize }
            : undefined
        }
        active={active}
        keysInBox={keysInBox}
      />
      {kind === "video" && subsNote && (
        <p role="status" className="media-subs-note" data-testid="media-subs-none">
          {t("files.media.subtitlesNone")}
        </p>
      )}
    </div>
  );
}
