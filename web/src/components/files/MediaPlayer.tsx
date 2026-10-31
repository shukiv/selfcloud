// SPDX-License-Identifier: AGPL-3.0-or-later

import { useLayoutEffect, useRef, useState } from "react";
import { Gate, NO_RENEWALS, PROGRESS_SECONDS, afterProgress, mediaVerdict, takeRenewal, type Renewals } from "../../lib/files/media";

// MediaPlayer plays one video or audio file from a signed link. The link ends at a fixed time, and the server refuses
// every request for it after that, so a seek or a resume past the end fails. Then the player asks for a new link and
// goes on at the same place, playing or paused as it was. Its decisions read no clock (lib/files/media.ts): a failure
// is renewed once if playback had not moved on, and a stall is renewed a few times at most. Only a media error reaches
// onFail; a stall never does, and the player simply keeps buffering when it has no links left to ask for.
// The owner of the player keys it by file, so moving to another file unmounts this one, which pauses it.

export interface Link {
  url: string;
}

export type MediaFailure = { kind: "unsupported" } | { kind: "error"; err: unknown };

interface Props {
  kind: "video" | "audio";
  name: string;
  link: Link;
  // mint asks for a new link to the same file. It may throw; the player reports the throw as an error.
  mint: () => Promise<Link>;
  onFail: (f: MediaFailure) => void;
}

// STALL_MS is how long a player may wait for data while playing before it asks for a new link. Some browsers report a
// refused range as a stall and no error at all.
const STALL_MS = 4000;

export default function MediaPlayer({ kind, name, link, mint, onFail }: Props) {
  const el = useRef<HTMLMediaElement | null>(null);
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
      el.current?.pause();
    };
  }, []);

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

  const onLoaded = () => {
    const media = el.current;
    const r = resume.current;
    if (!media || !r) return;
    resume.current = null;
    media.currentTime = r.at;
    if (r.play) void media.play().catch(() => undefined);
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
    if (media) renewals.current = afterProgress(renewals.current, media.currentTime);
  };

  const attach = (node: HTMLMediaElement | null) => {
    el.current = node;
  };
  const handlers = {
    onError,
    onLoadedMetadata: onLoaded,
    onWaiting,
    onPlaying: clearStall,
    onCanPlay: clearStall,
    onEmptied: clearStall,
    onTimeUpdate: onTime,
  };
  // A timeline reads left to right in every language, so the controls are not mirrored.
  return kind === "video" ? (
    <video ref={attach} src={current.url} aria-label={name} controls dir="ltr" style={{ maxWidth: "100%", maxHeight: "100%" }} {...handlers} />
  ) : (
    <audio ref={attach} src={current.url} aria-label={name} controls dir="ltr" {...handlers} />
  );
}
