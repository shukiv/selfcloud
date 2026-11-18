// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Button, Modal, Segmented, Slider, Switch } from "antd";
import { useT } from "../../lib/i18n";
import { BANDS, PRESETS, bandLabel, dbLabel, gainsOf, presetOf, readEq, writeEq, type Eq, type PresetName } from "../../lib/files/equalizer";
import { session } from "../../lib/session";
import { useStoreSelect } from "../../lib/store";

// MediaEqualizer is the equalizer panel of the player's More menu (decision 232): an On switch, a preset choice and
// ten sliders, one per band from 31 Hz to 16 kHz, each -12 to +12 dB on the step of 1 with its value shown. The bar's
// More and the phone album page's More both open it, in the bar's popupHost, so it shows in full screen too. Every
// change is kept per person (lib/files/equalizer.ts); the Web Audio graph (lib/files/eqGraph.ts) is loaded only by
// dynamic import, when the panel opens or a kept On choice meets a playing element, so it stays out of the first load.

interface Props {
  media: HTMLMediaElement | null;
  open: boolean;
  onClose: () => void;
  popupHost: () => HTMLElement;
}

// useEqOnPlay applies a kept On choice to a new element at its first play event, only through attach: an element is
// never routed into a context that is not running, and a refused attach leaves it playing plain. An element already
// playing when the hook meets it takes the choice at once, the same way.
export function useEqOnPlay(media: HTMLMediaElement | null, userId: string | null): void {
  useEffect(() => {
    if (!media) return;
    const eq = readEq(userId);
    if (!eq.on) return;
    let done = false;
    const apply = () => {
      if (done) return;
      done = true;
      void import("../../lib/files/eqGraph")
        .then(async (g) => {
          if (await g.attach(media)) g.setGains(media, eq.gains);
        })
        .catch(() => {});
    };
    media.addEventListener("play", apply);
    if (!media.paused) apply();
    return () => media.removeEventListener("play", apply);
  }, [media, userId]);
}

export default function MediaEqualizer({ media, open, onClose, popupHost }: Props) {
  const { t } = useT();
  const userId = useStoreSelect(session, (s) => s?.user.id ?? null);
  const [eq, setEq] = useState<Eq>(() => readEq(userId));
  const [blocked, setBlocked] = useState(false);
  const graphRef = useRef<Promise<typeof import("../../lib/files/eqGraph")> | null>(null);
  const graph = () => (graphRef.current ??= import("../../lib/files/eqGraph"));

  // The choice belongs to the person: a change of person reads their own kept one.
  useEffect(() => {
    setEq(readEq(userId));
    setBlocked(false);
  }, [userId]);

  // Opening the panel reads the kept choice afresh and loads the graph, so a click finds it ready.
  useEffect(() => {
    if (!open) return;
    setEq(readEq(userId));
    setBlocked(false);
    void graph().catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, userId]);

  // change keeps the choice at once and applies it on this click: On attaches and sets the gains; Off sets all 0 (the
  // graph stays, it cannot be undone for an element). A refused attach says so in one line; the switch stays usable.
  const change = (next: Eq) => {
    setEq(next);
    writeEq(userId, next);
    if (!media) return;
    if (next.on) {
      void graph()
        .then(async (g) => {
          const ok = await g.attach(media);
          setBlocked(!ok);
          if (ok) g.setGains(media, next.gains);
        })
        .catch(() => setBlocked(true));
    } else {
      void graph()
        .then((g) => g.setGains(media, gainsOf(next)))
        .catch(() => {});
    }
  };

  const preset = presetOf(eq.gains);
  const presetOptions = [
    { label: t("files.media.equalizerFlat"), value: "flat" },
    { label: t("files.media.equalizerBass"), value: "bass" },
    { label: t("files.media.equalizerTreble"), value: "treble" },
    { label: t("files.media.equalizerVocal"), value: "vocal" },
    ...(preset === "custom" ? [{ label: t("files.media.equalizerCustom"), value: "custom" }] : []),
  ];

  return (
    <Modal
      open={open}
      title={t("files.media.equalizerTitle")}
      maskClosable={false}
      onCancel={onClose}
      width={360}
      getContainer={popupHost}
      footer={[
        <Button key="reset" onClick={() => change({ ...eq, gains: [...PRESETS.flat] })}>
          {t("files.media.equalizerReset")}
        </Button>,
        <Button key="close" type="primary" onClick={onClose}>
          {t("common.close")}
        </Button>,
      ]}
    >
      <div className="media-eq-row">
        <span>{t("files.media.equalizerOn")}</span>
        <Switch checked={eq.on} aria-label={t("files.media.equalizerOn")} onChange={(on) => change({ ...eq, on })} />
      </div>
      <Segmented
        block
        aria-label={t("files.media.equalizerTitle")}
        value={preset}
        options={presetOptions}
        onChange={(v) => {
          if (v !== "custom") change({ ...eq, gains: [...PRESETS[v as PresetName]] });
        }}
      />
      {BANDS.map((hz, i) => (
        <div key={hz}>
          <div className="media-eq-row">
            <span>{bandLabel(hz)}</span>
            <span className="media-eq-value" dir="ltr">
              {dbLabel(eq.gains[i])}
            </span>
          </div>
          <Slider
            className="media-eq-slider"
            min={-12}
            max={12}
            step={1}
            value={eq.gains[i]}
            tooltip={{ open: false }}
            ariaLabelForHandle={bandLabel(hz)}
            ariaValueTextFormatterForHandle={(v: number) => `${v} dB`}
            onChange={(v) => change({ ...eq, gains: eq.gains.map((g, j) => (j === i ? v : g)) })}
          />
        </div>
      ))}
      {blocked && <p className="media-eq-blocked">{t("files.media.equalizerNoSound")}</p>}
    </Modal>
  );
}
