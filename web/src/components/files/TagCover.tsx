// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Film, Music } from "lucide-react";
import { thumbnailPath, watchThumbnail } from "../../lib/files/thumbnail";
import { getTags, markCover, subscribeTags } from "../../lib/files/tags";
import type { Node } from "../../lib/files/types";
import Icon from "./Icon";

// TagCover is the cover of an audio file, the picture its server thumbnail shows (decision 56). The box has its size from the
// first render, with a plain icon under the picture, so nothing moves when the cover arrives. The image is decorative: the title
// beside it says what the file is. enabled says a cover is asked for: a player asks for its file's cover, and a playlist row asks
// only when the file is known to have one (the listing's has_thumbnail, or a cover seen in this session), so a long list makes
// no covers of its own.
export default function TagCover({
  nod,
  kind,
  size,
  enabled,
  node,
}: {
  nod: string;
  kind: "audio" | "video";
  size: number;
  enabled: boolean;
  // node, when given, makes the placeholder the file's coloured type icon (decision 72), not the outline note.
  node?: Pick<Node, "kind" | "mime_type" | "name">;
}) {
  // shown is the cover and the node it belongs to. A cover is shown only for its own node, so when nod changes the previous
  // file's picture is not drawn beside the new title, not even for one frame before the effect runs.
  const [shown, setShown] = useState<{ nod: string; src: string } | null>(null);
  const src = shown !== null && shown.nod === nod ? shown.src : null;
  // loaded ends the browser's load of a redirected cover, which holds a request slot until it does (as Thumb does).
  const loaded = useRef<(() => void) | null>(null);
  useEffect(() => {
    setShown(null);
    if (!enabled) return;
    // The thumbnail watch asks until the cover is ready, or until it is known there is none; it stops with the row.
    const stop = watchThumbnail(thumbnailPath(nod, 256), {
      image: (s) => {
        loaded.current = null;
        markCover(nod);
        setShown({ nod, src: s });
      },
      redirect: (s, done) => {
        loaded.current = done;
        markCover(nod);
        setShown({ nod, src: s });
      },
      none: () => setShown(null),
    });
    return () => {
      stop();
      loaded.current = null;
    };
  }, [nod, enabled]);
  const Note = kind === "video" ? Film : Music;
  return (
    <div
      className="tag-cover"
      data-testid="tag-cover"
      data-kind={kind}
      data-cover={src ? "yes" : "no"}
      style={{ width: size, height: size, flex: `0 0 ${size}px`, display: "flex", alignItems: "center", justifyContent: "center", overflow: "hidden" }}
    >
      {src ? (
        <img
          src={src}
          alt=""
          draggable={false}
          onLoad={() => loaded.current?.()}
          onError={() => {
            setShown(null);
            loaded.current?.();
          }}
          style={{ width: "100%", height: "100%", objectFit: "cover" }}
        />
      ) : (
        node ? <Icon kind={kind} size={size} node={node} /> : <Note size={Math.round(size / 2)} aria-hidden="true" />
      )}
    </div>
  );
}

// useTagState reads what the tags cache knows of a node, and follows its changes.
export function useTagState(nod: string) {
  return useSyncExternalStore(subscribeTags, () => getTags(nod), () => getTags(nod));
}
