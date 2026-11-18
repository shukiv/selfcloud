// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { App, Button, Dropdown, Modal, Segmented, Slider, type MenuProps } from "antd";
import type { MenuItemType } from "antd/es/menu/interface";
import { Ellipsis } from "lucide-react";
import IconButton from "../IconButton";
import { useT } from "../../lib/i18n";
import { useMT } from "../../lib/media/i18n";
import { formatTime } from "../../lib/files/mediaKeys";
import { getSleep, setSleep, subscribeSleep, type SleepChoice } from "../../lib/files/sleepTimer";
import { DEFAULT_PICTURE, pictureStyle, readPicture, writePicture, type Picture } from "../../lib/files/picture";
import { session } from "../../lib/session";
import { useStoreSelect } from "../../lib/store";
import type { Marks } from "../../lib/files/bookmarks";

// MediaExtras is the player's More menu: one icon that holds the extras the bar has no room for, so later player
// extras join one menu instead of adding buttons. It draws the A-B loop, which belongs to the file on screen, and the
// sleep timer, whose choice lives in lib/files/sleepTimer.ts because it outlives one element (the queue moves to a new
// element while a timed choice keeps counting). The popup opens in the bar's popupHost, so it shows in full screen.
// A video also gains Picture (its fit and the three percentages, kept per person in lib/files/picture.ts) and, when
// the element exposes more than one audio track, the Audio track submenu. The picture outlives the element; the
// tracks belong to the file on screen and nothing of them is kept for the next one. Audio is never styled.

interface Props {
  media: HTMLMediaElement | null;
  popupHost: () => HTMLElement;
  // marks, when the person may keep bookmarks (decision 218), adds Add a bookmark and Bookmarks (n) to the menu.
  marks?: Marks;
}

// MIN_LOOP is the shortest A-B loop accepted: half a second.
const MIN_LOOP = 0.5;

// SLEEP_KEYS maps the menu's sleep items to the store's choices.
const SLEEP_KEYS: Record<string, SleepChoice> = {
  "sleep-off": "off",
  "sleep-15": 15,
  "sleep-30": 30,
  "sleep-45": 45,
  "sleep-60": 60,
  "sleep-end": "end",
};

// ElAudioTrack is one track of the element's own audioTracks list, which only some browsers expose; the menu is
// feature-detected on it, and nothing is kept between files.
interface ElAudioTrack {
  label: string;
  language: string;
  enabled: boolean;
}

type MediaWithTracks = HTMLMediaElement & { audioTracks?: ArrayLike<ElAudioTrack> };

// TrackItem is one entry of the Audio track submenu: the element's track at its index, named and checked.
interface TrackItem {
  key: string;
  label: string;
  enabled: boolean;
}

// TrackRadioItem is a track entry of the radio kind: it carries its role and its checked state, which the menu's own
// types do not name.
type TrackRadioItem = MenuItemType & { role: "menuitemradio"; "aria-checked": boolean };

// audioListOf reads the element's own track list, or null when the element exposes none.
function audioListOf(media: HTMLMediaElement | null): ArrayLike<ElAudioTrack> | null {
  if (!media || !("audioTracks" in media)) return null;
  return (media as MediaWithTracks).audioTracks ?? null;
}

export default function MediaExtras({ media, popupHost, marks }: Props) {
  const { t } = useT();
  const mt = useMT();
  const { message } = App.useApp();
  const [loop, setLoop] = useState<{ a: number | null; b: number | null }>({ a: null, b: null });
  const loopRef = useRef(loop);
  loopRef.current = loop;
  const srcRef = useRef("");
  const [sleep, setSleepState] = useState(getSleep());
  const [open, setOpen] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => subscribeSleep(() => setSleepState(getSleep())), []);

  const userId = useStoreSelect(session, (s) => s?.user.id ?? null);
  const isVideo = media?.tagName === "VIDEO";
  const [picture, setPicture] = useState<Picture>(() => readPicture(userId));
  const [picOpen, setPicOpen] = useState(false);
  const [tracks, setTracks] = useState<TrackItem[]>([]);

  // The picture belongs to the person: a change of person reads their own kept choice.
  useEffect(() => {
    setPicture(readPicture(userId));
  }, [userId]);

  // The panel belongs to the element on screen: a new element closes it.
  useEffect(() => {
    setPicOpen(false);
  }, [media]);

  // The kept choice styles the video element at once and on every change; a new element takes it too. Audio is never
  // styled, and an element that leaves the player is left plain.
  useEffect(() => {
    if (!media || media.tagName !== "VIDEO") return;
    const s = pictureStyle(picture);
    media.style.objectFit = s.objectFit;
    media.style.filter = s.filter;
    return () => {
      media.style.objectFit = "";
      media.style.filter = "";
    };
  }, [media, picture]);

  // The audio tracks belong to the element: they are read on mount and on every loadedmetadata, and nothing is kept
  // between files. One track or none draws no submenu.
  useEffect(() => {
    const read = () => {
      const list = audioListOf(media);
      if (!list || list.length < 2) {
        setTracks([]);
        return;
      }
      const items: TrackItem[] = [];
      for (let i = 0; i < list.length; i++) {
        const tr = list[i];
        items.push({
          key: `audio-${i}`,
          label: tr.label || tr.language || t("files.media.audioTrackN", { n: i + 1 }),
          enabled: tr.enabled,
        });
      }
      setTracks(items);
    };
    read();
    media?.addEventListener("loadedmetadata", read);
    return () => media?.removeEventListener("loadedmetadata", read);
  }, [media, t]);

  // The loop belongs to the file: a new element clears it, and so does a change of currentSrc on the same element.
  // While A and B are set, every timeupdate at or past B seeks back to A. A seek by the person outside A..B keeps the
  // loop, since only reaching B returns to A.
  useEffect(() => {
    setLoop({ a: null, b: null });
    srcRef.current = media?.currentSrc ?? "";
    if (!media) return;
    const onTime = () => {
      if (media.currentSrc !== srcRef.current) {
        srcRef.current = media.currentSrc;
        setLoop({ a: null, b: null });
        return;
      }
      const { a, b } = loopRef.current;
      if (a !== null && b !== null && media.currentTime >= b) media.currentTime = a;
    };
    media.addEventListener("timeupdate", onTime);
    return () => media.removeEventListener("timeupdate", onTime);
  }, [media]);

  // The time left counts down while the menu is open and a timed choice runs, at least once a second.
  useEffect(() => {
    if (!open || sleep.endsAt === null) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [open, sleep.endsAt]);

  const setA = () => {
    if (!media) return;
    setLoop({ a: media.currentTime, b: null });
  };
  const setB = () => {
    if (!media || loop.a === null) return;
    if (media.currentTime - loop.a < MIN_LOOP) {
      message.warning(t("files.media.loopTooShort"));
      return;
    }
    setLoop({ a: loop.a, b: media.currentTime });
  };

  // changePicture applies the choice at once and keeps it under the person's own key.
  const changePicture = (p: Picture) => {
    setPicture(p);
    writePicture(userId, p);
  };

  // pickTrack enables the chosen track and disables the others, then re-reads the checked state.
  const pickTrack = (key: string) => {
    const list = audioListOf(media);
    if (!list) return;
    const idx = Number(key.slice("audio-".length));
    if (!Number.isInteger(idx) || idx < 0 || idx >= list.length) return;
    for (let i = 0; i < list.length; i++) list[i].enabled = i === idx;
    setTracks((prev) => prev.map((tr, i) => ({ ...tr, enabled: i === idx })));
  };

  const onMenuClick: MenuProps["onClick"] = ({ key }) => {
    if (key === "loop-a") setA();
    else if (key === "loop-b") setB();
    else if (key === "loop-clear") setLoop({ a: null, b: null });
    else if (key === "bookmark-add") marks?.add();
    else if (key === "bookmarks") marks?.openList();
    else if (key === "picture") setPicOpen(true);
    else if (key.startsWith("audio-")) pickTrack(key);
    else if (key in SLEEP_KEYS) setSleep(SLEEP_KEYS[key]);
  };

  const loopState =
    loop.a === null
      ? t("files.media.loopOff")
      : loop.b === null
        ? t("files.media.loopA", { time: formatTime(loop.a) })
        : t("files.media.loopRange", { from: formatTime(loop.a), to: formatTime(loop.b) });

  const left = sleep.endsAt !== null ? Math.max(0, Math.ceil((sleep.endsAt - now) / 1000)) : 0;
  const sleepKey = `sleep-${sleep.choice}`;

  const items: MenuProps["items"] = [
    ...(marks
      ? [
          { key: "bookmark-add", label: mt.t("media.bookmarks.add"), disabled: !marks.ready || marks.full || !media },
          { key: "bookmarks", label: mt.t("media.bookmarks.count", { count: marks.items.length }) },
          { type: "divider" as const },
        ]
      : []),
    {
      key: "loop",
      label: t("files.media.loop"),
      children: [
        { key: "loop-state", label: loopState, disabled: true },
        { key: "loop-a", label: t("files.media.loopSetA") },
        { key: "loop-b", label: t("files.media.loopSetB"), disabled: loop.a === null },
        { key: "loop-clear", label: t("files.media.loopClear"), disabled: loop.a === null },
      ],
    },
    {
      key: "sleep",
      label: t("files.media.sleep"),
      children: [
        ...(sleep.endsAt !== null ? [{ key: "sleep-left", label: t("files.media.sleepLeft", { time: formatTime(left) }), disabled: true }] : []),
        { key: "sleep-off", label: t("files.media.sleepOff") },
        { key: "sleep-15", label: t("files.media.sleep15") },
        { key: "sleep-30", label: t("files.media.sleep30") },
        { key: "sleep-45", label: t("files.media.sleep45") },
        { key: "sleep-60", label: t("files.media.sleep60") },
        { key: "sleep-end", label: t("files.media.sleepEnd") },
      ],
    },
    ...(isVideo ? [{ key: "picture", label: t("files.media.picture") }] : []),
    ...(tracks.length > 0
      ? [
          {
            key: "audio-track",
            label: t("files.media.audioTrack"),
            children: tracks.map(
              (tr): TrackRadioItem => ({ key: tr.key, label: tr.label, role: "menuitemradio", "aria-checked": tr.enabled }),
            ),
          },
        ]
      : []),
  ];

  // One row of the panel: the control's name and its value in percent, then the slider itself.
  const pictureRow = (k: "brightness" | "contrast" | "saturation", label: string) => (
    <div key={k}>
      <div className="media-picture-row">
        <span>{label}</span>
        <span className="media-picture-value" dir="ltr">{`${picture[k]}%`}</span>
      </div>
      <Slider
        className="media-picture-slider"
        min={50}
        max={150}
        step={5}
        value={picture[k]}
        tooltip={{ open: false }}
        ariaLabelForHandle={label}
        ariaValueTextFormatterForHandle={(v: number) => `${v}%`}
        onChange={(v) => changePicture({ ...picture, [k]: v })}
      />
    </div>
  );

  return (
    <>
      <Dropdown
        trigger={["click"]}
        onOpenChange={setOpen}
        getPopupContainer={popupHost}
        menu={{
          items,
          selectable: true,
          selectedKeys: [sleepKey, ...tracks.filter((tr) => tr.enabled).map((tr) => tr.key)],
          onClick: onMenuClick,
          getPopupContainer: popupHost,
        }}
      >
        <IconButton
          className="media-btn"
          label={t("files.media.moreOptions")}
          icon={<Ellipsis size={18} aria-hidden="true" />}
          aria-haspopup="menu"
          disabled={!media}
          popupHost={popupHost}
        />
      </Dropdown>
      {isVideo && (
        <Modal
          open={picOpen}
          title={t("files.media.pictureTitle")}
          maskClosable={false}
          onCancel={() => setPicOpen(false)}
          width={360}
          getContainer={popupHost}
          footer={[
            <Button key="reset" onClick={() => changePicture(DEFAULT_PICTURE)}>
              {t("files.media.pictureReset")}
            </Button>,
            <Button key="close" type="primary" onClick={() => setPicOpen(false)}>
              {t("common.close")}
            </Button>,
          ]}
        >
          <div className="media-picture-row">
            <span>{t("files.media.pictureFit")}</span>
          </div>
          <Segmented
            block
            aria-label={t("files.media.pictureFit")}
            value={picture.fit}
            options={[
              { label: t("files.media.pictureFitContain"), value: "contain" },
              { label: t("files.media.pictureFitCover"), value: "cover" },
              { label: t("files.media.pictureFitFill"), value: "fill" },
            ]}
            onChange={(v) => changePicture({ ...picture, fit: v as Picture["fit"] })}
          />
          {pictureRow("brightness", t("files.media.pictureBrightness"))}
          {pictureRow("contrast", t("files.media.pictureContrast"))}
          {pictureRow("saturation", t("files.media.pictureSaturation"))}
        </Modal>
      )}
    </>
  );
}
