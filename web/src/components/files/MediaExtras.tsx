// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { App, Dropdown, type MenuProps } from "antd";
import { Ellipsis } from "lucide-react";
import IconButton from "../IconButton";
import { useT } from "../../lib/i18n";
import { formatTime } from "../../lib/files/mediaKeys";
import { getSleep, setSleep, subscribeSleep, type SleepChoice } from "../../lib/files/sleepTimer";

// MediaExtras is the player's More menu: one icon that holds the extras the bar has no room for, so later player
// extras join one menu instead of adding buttons. It draws the A-B loop, which belongs to the file on screen, and the
// sleep timer, whose choice lives in lib/files/sleepTimer.ts because it outlives one element (the queue moves to a new
// element while a timed choice keeps counting). The popup opens in the bar's popupHost, so it shows in full screen.

interface Props {
  media: HTMLMediaElement | null;
  popupHost: () => HTMLElement;
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

export default function MediaExtras({ media, popupHost }: Props) {
  const { t } = useT();
  const { message } = App.useApp();
  const [loop, setLoop] = useState<{ a: number | null; b: number | null }>({ a: null, b: null });
  const loopRef = useRef(loop);
  loopRef.current = loop;
  const srcRef = useRef("");
  const [sleep, setSleepState] = useState(getSleep());
  const [open, setOpen] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => subscribeSleep(() => setSleepState(getSleep())), []);

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

  const onMenuClick: MenuProps["onClick"] = ({ key }) => {
    if (key === "loop-a") setA();
    else if (key === "loop-b") setB();
    else if (key === "loop-clear") setLoop({ a: null, b: null });
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
  ];

  return (
    <Dropdown
      trigger={["click"]}
      onOpenChange={setOpen}
      getPopupContainer={popupHost}
      menu={{ items, selectable: true, selectedKeys: [sleepKey], onClick: onMenuClick, getPopupContainer: popupHost }}
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
  );
}
