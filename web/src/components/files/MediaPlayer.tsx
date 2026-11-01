// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Gate, NO_RENEWALS, PROGRESS_SECONDS, afterProgress, mediaVerdict, takeRenewal, type Renewals } from "../../lib/files/media";
import { refusedPlay } from "../../lib/files/mediaKeys";
import { LATE_START_S, SaveClock, startAt as placeToStart, type Resume } from "../../lib/files/resume";
import { placeCue, type SubtitleTrack } from "../../lib/files/subtitles";
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
// given to the element.

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
  // active says the viewer shows the player, so its keys are read. A player kept on the page for audio alone is not active.
  active?: boolean;
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
  active = true,
}: Props) {
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

  // The subtitle tracks are added to the element once it exists. The browser draws their cues; none is shown until picked.
  useEffect(() => {
    const media = mediaEl;
    if (!media || kind !== "video" || !tracks || tracks.length === 0 || typeof window.VTTCue === "undefined") return;
    const made: TextTrack[] = [];
    for (const t of tracks) {
      const tt = media.addTextTrack("subtitles", t.label);
      for (const c of t.cues) {
        try {
          const cue = new window.VTTCue(c.start, c.end, c.text);
          placeCue(cue, window.matchMedia?.("(pointer: coarse)").matches ?? false);
          tt.addCue(cue);
        } catch {
          // A cue the browser will not take is left out; the others show.
        }
      }
      tt.mode = "disabled";
      made.push(tt);
    }
    textTracks.current = made;
    setSubs({ labels: tracks.map((t) => t.label), active: -1 });
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
        <video ref={attach} src={current.url} aria-label={name} dir="ltr" loop={loop} playsInline className="media-picture" {...handlers} />
      ) : (
        <audio ref={attach} src={current.url} aria-label={name} dir="ltr" loop={loop} className="media-sound" {...handlers} />
      )}
      <MediaControls
        media={mediaEl}
        kind={kind}
        stage={stageEl}
        nav={nav}
        needPress={needPress}
        onNeedPress={setNeedPress}
        subtitles={kind === "video" && subs.labels.length > 0 ? { ...subs, onPick: pickTrack } : undefined}
        active={active}
      />
    </div>
  );
}
