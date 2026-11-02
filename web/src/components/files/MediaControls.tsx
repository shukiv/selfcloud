// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type FocusEvent, type ReactNode } from "react";
import { ConfigProvider, Select, Slider, theme as antdTheme } from "antd";
import { Maximize, Minimize, Pause, PictureInPicture2, Play, SkipBack, SkipForward, Volume2, VolumeX } from "lucide-react";
import IconButton from "../IconButton";
import { useT } from "../../lib/i18n";
import { readNonce } from "../../csp/nonce";
import { usePaletteOrLight } from "../../theme/theme";
import {
  HIDE_MS,
  SPEEDS,
  refusedPlay,
  VOLUME_STEP,
  bufferedEnd,
  clamp,
  formatTime,
  ignoredTarget,
  keyCommand,
  playerPrefs,
  saveVolumePrefs,
  type Command,
} from "../../lib/files/mediaKeys";

// MediaControls is the player's own control bar, in place of the browser's. It reads the element's state from the
// element's events and changes the element; it keeps no media state of its own beyond what the screen shows. The bar
// reads left to right in every language (dir="ltr" on the bar, and its labels are translated).
// On a video the bar hides while the video plays, and comes back on a pointer move or when keyboard focus is in it. It
// never hides while focus is in it. Audio shows its bar always, since there is no picture to uncover.
// The keys are read at the window, and a key typed into a field, or aimed at a control that takes it, is left alone
// (ignoredTarget). The volume, the mute and the speed are the person's choice and are kept for the next file (playerPrefs).

export interface Nav {
  next: () => void;
  previous: () => void;
}

// WebkitVideo is a video element with the iPhone's full screen, which is not in the standard type.
type WebkitVideo = HTMLVideoElement & { webkitEnterFullscreen?: () => void };

interface Snap {
  playing: boolean;
  time: number;
  duration: number;
  buffered: number;
  volume: number;
  muted: boolean;
  rate: number;
  full: boolean;
  pip: boolean;
}

// REPEATS are the commands a held key repeats: the seeks and the volume. The others act once per press.
const REPEATS = new Set<Command>(["back10", "forward10", "back5", "forward5", "volumeUp", "volumeDown"]);
const isSpace = (key: string) => key === " " || key === "Spacebar";

const EMPTY: Snap = { playing: false, time: 0, duration: 0, buffered: 0, volume: 1, muted: false, rate: 1, full: false, pip: false };

// The events that change what the bar shows.
const EVENTS = [
  "play",
  "pause",
  "ended",
  "timeupdate",
  "durationchange",
  "loadedmetadata",
  "progress",
  "volumechange",
  "ratechange",
  "seeked",
  "emptied",
  "enterpictureinpicture",
  "leavepictureinpicture",
];

interface Props {
  media: HTMLMediaElement | null;
  kind: "video" | "audio";
  // stage is the element that goes full screen: the video with its bar, so the bar stays on the screen.
  stage: HTMLElement | null;
  // nav, when there is a playlist, adds Previous and Next to the bar.
  nav?: Nav;
  // needPress says the browser refused to play for want of a press: the bar says so. onNeedPress sets or clears it.
  needPress?: boolean;
  onNeedPress?: (v: boolean) => void;
  // subtitles, when the video has subtitle files, is the menu of them: the labels, which one shows (-1 for none), and the pick.
  subtitles?: { labels: string[]; active: number; onPick: (i: number) => void };
  // active says whether the keys are read here. A player kept for audio alone, with its viewer closed, reads none.
  active?: boolean;
}

export default function MediaControls({ media, kind, stage, nav, needPress = false, onNeedPress, subtitles, active = true }: Props) {
  const { t } = useT();
  const p = usePaletteOrLight();
  const [snap, setSnap] = useState<Snap>(EMPTY);
  // drag is the seek position while the person drags the bar; the element moves when the drag ends.
  const [drag, setDrag] = useState<number | null>(null);
  const [shown, setShown] = useState(true);
  const root = useRef<HTMLDivElement | null>(null);
  const within = useRef(false);
  // pointed says a press landed on the bar and its focus has not come yet. A press gives a button the focus on a phone,
  // but that is not keyboard focus, so the bar still hides; only focus that came from the keyboard keeps it shown.
  const pointed = useRef(false);
  const playingRef = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const navRef = useRef(nav);
  navRef.current = nav;
  const needRef = useRef(onNeedPress);
  needRef.current = onNeedPress;
  // swallowSpaceUp is set by a Space the player took on its keydown, until that Space's keyup comes.
  const swallowSpaceUp = useRef(false);

  // read copies the element's state into the bar, and the volume, mute and speed into the player's preferences. Only a
  // change of volume or mute is written to the browser's storage: the time and the other events are read without a write.
  const read = (m: HTMLMediaElement) => {
    const changed = m.volume !== playerPrefs.volume || m.muted !== playerPrefs.muted;
    playerPrefs.volume = m.volume;
    playerPrefs.muted = m.muted;
    playerPrefs.rate = m.playbackRate;
    if (changed) saveVolumePrefs();
    setSnap({
      playing: !m.paused && !m.ended,
      time: m.currentTime,
      duration: Number.isFinite(m.duration) ? m.duration : 0,
      buffered: bufferedEnd(m.buffered, m.currentTime),
      volume: m.volume,
      muted: m.muted,
      rate: m.playbackRate,
      full: stage !== null && document.fullscreenElement === stage,
      pip: document.pictureInPictureElement === m,
    });
  };

  useEffect(() => {
    if (!media) return;
    // A new file takes the volume, mute and speed the person last chose.
    media.volume = playerPrefs.volume;
    media.muted = playerPrefs.muted;
    media.playbackRate = playerPrefs.rate;
    const on = () => read(media);
    // The file has started: a note that the browser waited for a press is no longer true.
    const started = () => needRef.current?.(false);
    for (const e of EVENTS) media.addEventListener(e, on);
    media.addEventListener("play", started);
    document.addEventListener("fullscreenchange", on);
    read(media);
    return () => {
      for (const e of EVENTS) media.removeEventListener(e, on);
      media.removeEventListener("play", started);
      document.removeEventListener("fullscreenchange", on);
    };
    // read is rebuilt on each render, and only the element and the stage it reads matter here.
  }, [media, stage]);

  playingRef.current = snap.playing;

  const clearTimer = () => {
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = null;
  };
  // wake shows the bar, and on a playing video starts the count to hide it again.
  const wake = () => {
    setShown(true);
    clearTimer();
    if (kind === "video" && playingRef.current && !within.current) {
      timer.current = setTimeout(() => {
        // Keyboard focus that came into the bar while the count ran keeps it shown.
        if (playingRef.current && !within.current) setShown(false);
      }, HIDE_MS);
    }
  };

  // A change of play state shows the bar: a paused video keeps it, a playing one counts to hide it.
  useEffect(() => {
    wake();
    // wake reads only refs and the kind, which the dependencies below cover.
  }, [snap.playing, kind]);

  // A pointer move or press on the picture shows the bar.
  useEffect(() => {
    if (!stage) return;
    const show = () => wake();
    stage.addEventListener("pointermove", show);
    stage.addEventListener("pointerdown", show);
    return () => {
      stage.removeEventListener("pointermove", show);
      stage.removeEventListener("pointerdown", show);
      clearTimer();
    };
  }, [stage, kind]);

  useEffect(() => () => clearTimer(), []);

  const seek = (m: HTMLMediaElement, to: number) => {
    const end = Number.isFinite(m.duration) ? m.duration : Infinity;
    m.currentTime = clamp(to, 0, end);
  };

  const togglePlay = (m: HTMLMediaElement) => {
    if (m.paused) m.play().catch((err: unknown) => onNeedPress?.(refusedPlay(err)));
    else m.pause();
  };

  // toggleFull puts the stage (the picture with its bar) in full screen. Where the page cannot do that, as on iPhone, the
  // video takes the system's own full screen, which the browser provides for a video element alone.
  const toggleFull = () => {
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => undefined);
    } else if (stage && typeof stage.requestFullscreen === "function") {
      void stage.requestFullscreen().catch(() => undefined);
    } else if (media && kind === "video" && typeof (media as WebkitVideo).webkitEnterFullscreen === "function") {
      (media as WebkitVideo).webkitEnterFullscreen?.();
    }
  };

  const togglePip = () => {
    if (!media) return;
    if (document.pictureInPictureElement) {
      void document.exitPictureInPicture().catch(() => undefined);
    } else if (kind === "video" && typeof (media as HTMLVideoElement).requestPictureInPicture === "function") {
      void (media as HTMLVideoElement).requestPictureInPicture().catch(() => undefined);
    }
  };

  // run does what a key or a button asks. It reads the element when the key comes, so it sees the current state.
  const run = (cmd: Command) => {
    const m = media;
    if (!m) return;
    switch (cmd) {
      case "toggle":
        togglePlay(m);
        break;
      case "back10":
        seek(m, m.currentTime - 10);
        break;
      case "forward10":
        seek(m, m.currentTime + 10);
        break;
      case "back5":
        seek(m, m.currentTime - 5);
        break;
      case "forward5":
        seek(m, m.currentTime + 5);
        break;
      case "volumeUp":
        m.volume = clamp(m.volume + VOLUME_STEP, 0, 1);
        m.muted = false;
        break;
      case "volumeDown":
        m.volume = clamp(m.volume - VOLUME_STEP, 0, 1);
        break;
      case "mute":
        m.muted = !m.muted;
        break;
      case "fullscreen":
        if (kind === "video") toggleFull();
        break;
      case "next":
        navRef.current?.next();
        break;
      case "previous":
        navRef.current?.previous();
        break;
    }
    // The bar and the preferences follow the change at once, not only when the element reports it.
    read(m);
  };

  // The window reads the keys. The handler is rebuilt when the element changes, so it always acts on the current one.
  useEffect(() => {
    if (!media || !active) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      const cmd = keyCommand(e);
      if (cmd === null || ignoredTarget(e.target, e.key)) return;
      e.preventDefault();
      // A key held down repeats. Seeks and volume follow the repeat; a toggle, a fullscreen or mute, and a step to another
      // file do once per press, so holding the key does not toggle or step over and over. The repeat is consumed here.
      if (e.repeat && !REPEATS.has(cmd)) return;
      if (isSpace(e.key)) swallowSpaceUp.current = true;
      wake();
      run(cmd);
    };
    // A Space the player took on its keydown is taken on its keyup too: a browser that clicks the focused button on the
    // keyup (Firefox does, Chrome does not) would otherwise press the button the pointer last hit.
    const onKeyUp = (e: KeyboardEvent) => {
      if (!isSpace(e.key) || !swallowSpaceUp.current) return;
      swallowSpaceUp.current = false;
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("keyup", onKeyUp);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("keyup", onKeyUp);
    };
    // run and wake read the element, the kind and the stage, which are the dependencies here.
  }, [media, kind, stage, active]);

  const onPointerDown = () => {
    pointed.current = true;
    // The focus a press gives comes in the same task. A press whose focus never comes is not held for a later key press.
    setTimeout(() => {
      pointed.current = false;
    }, 0);
    wake();
  };
  const onFocus = () => {
    if (pointed.current) {
      pointed.current = false;
      wake();
      return;
    }
    within.current = true;
    wake();
  };
  const onBlur = (e: FocusEvent) => {
    if (root.current && e.relatedTarget instanceof Node && root.current.contains(e.relatedTarget)) return;
    within.current = false;
    wake();
  };

  const m = media;
  const duration = snap.duration;
  // A popup (the speed list, a tooltip) opens on the page, except in full screen: there the page is not shown, so it opens
  // on the stage, which is what is shown. The stage clips nothing outside full screen, so the page is the right place.
  const popupHost = () => (stage !== null && document.fullscreenElement === stage ? stage : document.body);
  // A video's bar is a dark control set whatever the page's theme: the dark algorithm, with the bar's own colours. The bar
  // always reads left to right (direction ltr), so its sliders do too, in every language.
  const barTheme =
    kind === "video"
      ? {
          algorithm: antdTheme.darkAlgorithm,
          token: {
            colorPrimary: p.accent,
            colorBgContainer: "#000000",
            colorBgElevated: "#1f1f1f",
            colorText: "#ffffff",
            colorTextSecondary: "#d9d9d9",
            colorBorder: "#595959",
          },
        }
      : undefined;
  const shownTime = drag ?? snap.time;
  const pct = (v: number) => (duration > 0 ? `${Math.min(100, (v / duration) * 100)}%` : "0%");
  const hidden = kind === "video" && !shown;

  // Each control is an icon with its name as its tooltip (decision 68); the tooltip is drawn in the full-screen element.
  const button = (label: string, icon: ReactNode, onClick: () => void, extra: { pressed?: boolean; disabled?: boolean } = {}) => (
    <IconButton className="media-btn" label={label} pressed={extra.pressed} disabled={extra.disabled} onClick={onClick} icon={icon} popupHost={popupHost} />
  );

  return (
    <ConfigProvider direction="ltr" theme={barTheme} csp={{ nonce: readNonce() }}>
    <div
      ref={root}
      className={`media-controls${hidden ? " media-controls--hidden" : ""}`}
      style={kind === "audio" ? { background: p.card, color: p.text, borderColor: p.line } : undefined}
      role="group"
      aria-label={t("files.media.controls")}
      dir="ltr"
      data-testid="media-controls"
      onFocus={onFocus}
      onBlur={onBlur}
      onPointerDown={onPointerDown}
    >
      {needPress && (
        <p role="status" className="media-press" data-testid="media-press">
          {t("files.media.pressPlay")}
        </p>
      )}
      <div className="media-seek-row">
        <span className="media-time" data-testid="media-time">
          {formatTime(shownTime)}
        </span>
        <div className="media-seek">
          <div className="media-buffered" data-testid="media-buffered" style={{ width: pct(snap.buffered) }} />
          <Slider
            min={0}
            max={duration || 0}
            step={1}
            value={shownTime}
            disabled={!m || duration === 0}
            tooltip={{ formatter: (v) => formatTime(v ?? 0), getPopupContainer: popupHost }}
            ariaLabelForHandle={t("files.media.position")}
            ariaValueTextFormatterForHandle={(v: number) => formatTime(v)}
            onChange={(v) => setDrag(v)}
            onChangeComplete={(v) => {
              setDrag(null);
              if (m) seek(m, v);
            }}
          />
        </div>
        <span className="media-time" dir="ltr">
          {formatTime(duration)}
        </span>
      </div>

      <div className="media-buttons">
        {nav && button(t("files.media.previous"), <SkipBack size={18} aria-hidden="true" />, () => nav.previous())}
        {button(
          snap.playing ? t("files.media.pause") : t("files.media.play"),
          snap.playing ? <Pause size={18} aria-hidden="true" /> : <Play size={18} aria-hidden="true" />,
          () => m && togglePlay(m),
          { disabled: !m },
        )}
        {nav && button(t("files.media.next"), <SkipForward size={18} aria-hidden="true" />, () => nav.next())}

        <div className="media-volume">
          {button(
            snap.muted || snap.volume === 0 ? t("files.media.unmute") : t("files.media.mute"),
            snap.muted || snap.volume === 0 ? <VolumeX size={18} aria-hidden="true" /> : <Volume2 size={18} aria-hidden="true" />,
            () => m && run("mute"),
            { disabled: !m },
          )}
          <Slider
            className="media-volume-slider"
            min={0}
            max={1}
            step={VOLUME_STEP}
            value={snap.muted ? 0 : snap.volume}
            disabled={!m}
            tooltip={{ open: false }}
            ariaLabelForHandle={t("files.media.volume")}
            ariaValueTextFormatterForHandle={(v: number) => `${Math.round(v * 100)}%`}
            onChange={(v) => {
              if (!m) return;
              m.volume = v;
              m.muted = v === 0;
            }}
          />
        </div>

        <span className="media-spacer" />

        {kind === "video" && subtitles && (
          <Select
            size="small"
            className="media-subs"
            aria-label={t("files.media.subtitles")}
            value={subtitles.active}
            disabled={!m}
            popupMatchSelectWidth={false}
            getPopupContainer={popupHost}
            options={[{ value: -1, label: t("files.media.subtitlesOff") }, ...subtitles.labels.map((label, i) => ({ value: i, label }))]}
            onChange={(v: number) => subtitles.onPick(v)}
          />
        )}

        <Select
          size="small"
          className="media-speed"
          aria-label={t("files.media.speed")}
          value={snap.rate}
          disabled={!m}
          popupMatchSelectWidth={false}
          getPopupContainer={popupHost}
          options={SPEEDS.map((v) => ({ value: v, label: `${v}×` }))}
          onChange={(v: number) => {
            if (m) m.playbackRate = v;
          }}
        />

        {kind === "video" &&
          typeof document !== "undefined" &&
          document.pictureInPictureEnabled &&
          button(snap.pip ? t("files.media.pipExit") : t("files.media.pip"), <PictureInPicture2 size={18} aria-hidden="true" />, togglePip, {
            pressed: snap.pip,
            disabled: !m,
          })}
        {kind === "video" &&
          button(
            snap.full ? t("files.media.exitFullscreen") : t("files.media.fullscreen"),
            snap.full ? <Minimize size={18} aria-hidden="true" /> : <Maximize size={18} aria-hidden="true" />,
            toggleFull,
            { pressed: snap.full },
          )}
      </div>
    </div>
    </ConfigProvider>
  );
}
