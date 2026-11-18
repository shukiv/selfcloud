// SPDX-License-Identifier: AGPL-3.0-or-later

// The Web Audio half of the player's equalizer (decision 232): one shared AudioContext for the page, made lazily, and
// one chain per element — source, ten BiquadFilterNodes (a low shelf at 31 Hz, eight peaking bands with Q 1.41, a high
// shelf at 16 kHz), destination. This module is loaded only by dynamic import, when the panel opens or a kept On
// choice meets a playing element, so it stays out of the first load.
//
// Three rules keep the sound alive. A source is made only on a context that is running: a suspended context would
// leave the element silent, so attach resumes first and gives up (the element plays plain) when it stays suspended.
// A source is made at most once per element: a second createMediaElementSource throws in browsers, and the chain
// cannot be undone, so Off is all gains at 0, not a removed graph. And a source is made only for an element whose
// source is on this page's origin: a cross-origin element through the graph outputs silence.

import { BANDS } from "./equalizer";

let ctx: AudioContext | null = null;
const sources = new WeakMap<HTMLMediaElement, MediaElementAudioSourceNode>();
const chains = new WeakMap<HTMLMediaElement, BiquadFilterNode[]>();
// The attached elements are held weakly: the state-change watch asks whether any of them plays, and an element the
// player let go of is not kept alive by this module.
const attached = new Set<WeakRef<HTMLMediaElement>>();

// context makes the page's one AudioContext on first use, with the watch that tries resume once when the context
// leaves running while an attached element plays.
function context(): AudioContext {
  if (!ctx) {
    const c = new AudioContext();
    c.onstatechange = () => {
      if (c.state === "running") return;
      let playing = false;
      for (const ref of attached) {
        const m = ref.deref();
        if (!m) {
          attached.delete(ref);
          continue;
        }
        if (!m.paused && !m.ended) playing = true;
      }
      if (playing) void c.resume().catch(() => {});
    };
    ctx = c;
  }
  return ctx;
}

// attach routes the element through the equalizer's chain, or returns false and does nothing: an element of another
// origin, or a context that is not running after one resume try, plays plain instead. A second call on the same
// element reuses its one source.
export async function attach(media: HTMLMediaElement): Promise<boolean> {
  const src = media.currentSrc || media.src;
  if (new URL(src, location.href).origin !== location.origin) return false;
  const c = context();
  await c.resume();
  if (c.state !== "running") return false;
  if (!sources.has(media)) {
    const source = c.createMediaElementSource(media);
    sources.set(media, source);
    const filters = BANDS.map((hz, i) => {
      const f = c.createBiquadFilter();
      f.type = i === 0 ? "lowshelf" : i === BANDS.length - 1 ? "highshelf" : "peaking";
      f.frequency.value = hz;
      if (f.type === "peaking") f.Q.value = 1.41;
      f.gain.value = 0;
      return f;
    });
    let node: AudioNode = source;
    for (const f of filters) {
      node.connect(f);
      node = f;
    }
    node.connect(c.destination);
    chains.set(media, filters);
    attached.add(new WeakRef(media));
  }
  return true;
}

// setGains sets each filter's gain to the choice's; Off passes all 0. An element that was never attached is left
// alone: it plays plain, and there is nothing to set.
export function setGains(media: HTMLMediaElement, gains: number[]): void {
  const filters = chains.get(media);
  if (!filters) return;
  for (let i = 0; i < filters.length; i++) {
    filters[i].gain.value = gains[i] ?? 0;
  }
}
