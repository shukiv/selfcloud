// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useState } from "react";
import { Music } from "lucide-react";
import { thumbnailPath, watchThumbnail } from "../../lib/files/thumbnail";
import { displayArtist, displayTitle, markCover } from "../../lib/files/tags";
import { folderCovers, folderKeyOf, pickFolderCover, type Listing } from "../../lib/files/folderCover";
import { session } from "../../lib/session";
import { get } from "../../lib/store";
import { useTagState } from "./TagCover";
import { inlineLink } from "./Preview";
import type { Node } from "../../lib/files/types";

// MediaArt is the stage of an audio file (slice x6, decision 63): its cover, large, with the name and the tags under it. The
// cover is the file's own picture when it has one (the server's thumbnail of the file, the largest size first, then the
// smaller one a server configured without the larger one answers). A file with none takes the picture of its own folder
// (lib/files/folderCover.ts). With neither, a tinted box with a music icon holds the place, so the page is never blank.
// Every answer is kept for the file it is for: a cover of another track is never drawn, not even for one frame.

// COVER_SIZES are the sizes asked, the largest first. 1024 is the largest of the default thumb_sizes (256 and 1024).
export const COVER_SIZES = [1024, 256] as const;

// Shown is a picture for one file: the address to draw, and the release of a redirected load (null when the address is a blob).
interface Shown {
  nod: string;
  src: string;
  done: (() => void) | null;
}

// watchCover asks the server for a file's picture at each size in turn, until one answers with the image; onNone runs when no
// size has one. It returns the function that stops the asking.
export function watchCover(nod: string, onSrc: (src: string, done: (() => void) | null) => void, onNone: () => void): () => void {
  let stopped = false;
  let stopCurrent: () => void = () => undefined;
  const at = (i: number) => {
    if (i >= COVER_SIZES.length) {
      onNone();
      return;
    }
    stopCurrent = watchThumbnail(thumbnailPath(nod, COVER_SIZES[i]), {
      image: (src) => onSrc(src, null),
      redirect: (src, done) => onSrc(src, done),
      none: () => {
        if (!stopped) at(i + 1);
      },
    });
  };
  at(0);
  return () => {
    stopped = true;
    stopCurrent();
  };
}

export default function MediaArt({ node }: { node: Node }) {
  const tags = useTagState(node.id);
  const nod = node.id;
  // embedded is the file's own picture: "pending" until the server has answered, then yes (a picture) or no.
  const [embedded, setEmbedded] = useState<{ nod: string; state: "pending" | "yes" | "no"; shown?: Shown }>({ nod, state: "pending" });
  // listing is the folder's listing (folderCovers), for the folder the file is in: every track of the folder shares it.
  const folderKey = folderKeyOf(node);
  const [listing, setListing] = useState<{ key: string; value: Listing | null } | null>(null);
  // folderShown is the folder's picture, by the file's id. It is kept while the same picture is picked, so a track change
  // inside the album neither drops it nor asks for it again.
  const [folderShown, setFolderShown] = useState<{ id: string; src: string; done: (() => void) | null } | null>(null);

  // A visitor of a share page has no session: no picture is asked for, and the icon holds the place (the server refuses those
  // requests, and a share's pictures are not decided here).
  const signedIn = get(session) !== null;

  useEffect(() => {
    setEmbedded({ nod, state: "pending" });
    if (!signedIn) {
      setEmbedded({ nod, state: "no" });
      return;
    }
    const stop = watchCover(
      nod,
      (src, done) => {
        // A cover seen here is recorded for the playlist rows and the lock screen, as the stage's cover was before (TagCover).
        markCover(nod);
        setEmbedded({ nod, state: "yes", shown: { nod, src, done } });
      },
      () => setEmbedded({ nod, state: "no" }),
    );
    return () => stop();
  }, [nod, signedIn]);

  // The folder is read only for a file with no picture of its own, and only with a session (folderCovers keeps the listing).
  useEffect(() => {
    if (!signedIn || embedded.nod !== nod || embedded.state !== "no") return;
    let live = true;
    void folderCovers.get(node).then((value) => {
      if (live) setListing({ key: folderKey, value });
    });
    return () => {
      live = false;
    };
    // The file's folder is what the read is for; the rest of the node does not change its answer.
  }, [folderKey, nod, signedIn, embedded.nod, embedded.state]);

  // The album tag is read when it is known, so the rule takes it as soon as the tags arrive; a file with no tags has no album.
  const album = tags?.state === "ready" ? tags.album : undefined;
  const pick = useMemo(() => {
    if (listing === null || listing.key !== folderKey || listing.value === null) return null;
    return pickFolderCover(listing.value.items, { album, folderName: listing.value.folderName, complete: listing.value.complete });
  }, [listing, folderKey, album]);
  const pickId = pick?.id ?? null;
  const pickName = pick?.name ?? "";

  // The folder's picture: a webp has no thumbnail, so its signed inline link is shown; the others take the server's thumbnail.
  // The effect is keyed by the picked file, not by the track: the same picture across the album is asked for once.
  useEffect(() => {
    if (pickId === null) return;
    let live = true;
    let stop: () => void = () => undefined;
    if (/\.webp$/i.test(pickName)) {
      void inlineLink(pickId).then(
        (link) => {
          if (live && link !== null) setFolderShown({ id: pickId, src: link.url, done: null });
        },
        () => undefined,
      );
    } else {
      stop = watchCover(
        pickId,
        (src, done) => {
          if (live) setFolderShown({ id: pickId, src, done });
        },
        () => undefined,
      );
    }
    return () => {
      live = false;
      stop();
    };
  }, [pickId, pickName]);

  // The picture on screen is the file's own, or the folder's for a file with none; a picture of another file is not shown.
  const own = embedded.nod === nod && embedded.state === "yes" ? embedded.shown : undefined;
  const fromFolder = own === undefined && folderShown !== null && folderShown.id === pickId ? folderShown : undefined;
  const src = own?.src ?? fromFolder?.src ?? null;

  const title = displayTitle(node.name, tags);
  const artist = displayArtist(tags);
  return (
    <div className="media-art" data-testid="media-art">
      <div className="media-cover-box" data-testid="media-cover" data-cover={src ? "yes" : "no"} data-source={own ? "file" : fromFolder ? "folder" : "none"}>
        {src ? (
          <img
            src={src}
            alt=""
            draggable={false}
            onLoad={() => (own ?? fromFolder)?.done?.()}
            onError={() => {
              (own ?? fromFolder)?.done?.();
              setEmbedded((e) => (e.nod === nod && e.shown?.src === src ? { nod, state: "no" } : e));
              setFolderShown((f) => (f !== null && f.src === src ? null : f));
            }}
          />
        ) : (
          <div className="media-cover-empty" aria-hidden="true">
            <Music size={96} />
          </div>
        )}
      </div>
      <div className="media-art-text">
        <p className="media-art-name">
          <bdi dir="auto">{title}</bdi>
        </p>
        <p className="media-art-artist" dir="auto" data-testid="media-artist">
          {artist ?? ""}
        </p>
      </div>
    </div>
  );
}
