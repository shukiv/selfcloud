// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type ReactNode } from "react";
import { theme as antdTheme } from "antd";
import { thumbnailPath, watchThumbnail, type ThumbSink } from "../../lib/files/thumbnail";

// Thumb shows an image's thumbnail in a box of the grid's height, and the fallback (the file-type icon) until there is
// one. version is what the picture is made from (the file's checksum, or its etag): when it changes, the thumbnail is
// asked for again, so a new version or a restore shows its own picture.
//
// The tile asks while it is in view. When it scrolls out before it has an image, its asks stop, and they resume when it
// comes back into view. Once an image is shown it stays, and nothing more is asked. The image is decorative: the name
// beside it says what it is.
export default function Thumb({ nod, version, fallback }: { nod: string; version: string; fallback: ReactNode }) {
  const { token } = antdTheme.useToken();
  const box = useRef<HTMLDivElement>(null);
  const [src, setSrc] = useState<string | null>(null);
  // loaded ends the browser's load of a redirected image, which holds a request slot until it does.
  const loaded = useRef<(() => void) | null>(null);

  useEffect(() => {
    setSrc(null);
    const el = box.current;
    if (!el) return;
    let stop: (() => void) | null = null;
    // shown is set once an image (or a redirect to one) is on the page. From then on the tile asks for nothing more.
    let shown = false;
    const sink: ThumbSink = {
      image: (s) => {
        shown = true;
        loaded.current = null;
        setSrc(s);
      },
      redirect: (s, done) => {
        shown = true;
        loaded.current = done;
        setSrc(s);
      },
      none: () => setSrc(null),
    };
    const start = () => {
      if (stop === null && !shown) stop = watchThumbnail(thumbnailPath(nod), sink);
    };
    const halt = () => {
      if (!shown && stop !== null) {
        stop();
        stop = null;
      }
    };
    // Without an IntersectionObserver (old browsers, and tests that stub none), the thumbnail is asked for at once.
    const io: IntersectionObserver | null =
      typeof IntersectionObserver === "undefined"
        ? null
        : new IntersectionObserver(
            (entries) => {
              const last = entries[entries.length - 1];
              if (!last) return;
              if (last.isIntersecting) start();
              else halt();
            },
            { rootMargin: "200px" },
          );
    if (io) io.observe(el);
    else start();
    return () => {
      io?.disconnect();
      stop?.();
      stop = null;
    };
  }, [nod, version]);

  return (
    <div
      ref={box}
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        width: "100%",
        height: 96,
        overflow: "hidden",
        borderRadius: token.borderRadiusSM,
        background: src ? token.colorFillQuaternary : undefined,
      }}
    >
      {src ? (
        <img
          src={src}
          alt=""
          draggable={false}
          onLoad={() => loaded.current?.()}
          // A redirected image the browser cannot load (the file was swept, or the content origin refused) shows the icon.
          onError={() => {
            setSrc(null);
            loaded.current?.();
          }}
          style={{ maxWidth: "100%", maxHeight: "100%", objectFit: "contain" }}
        />
      ) : (
        fallback
      )}
    </div>
  );
}
