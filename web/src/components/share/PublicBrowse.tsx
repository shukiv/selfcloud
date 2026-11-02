// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Alert, Button } from "antd";
import { useT } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import type { Node } from "../../lib/files/types";
import { visitorChildren, visitorLink, visitorNode } from "../../lib/sharing/api";
import { crumbsTo, previewKind, type NodeRef } from "../../lib/sharing/anyone";
import { kindOf } from "../../lib/files/kind";
import { Pager } from "../../lib/files/pager";
import type { Link } from "../files/MediaPlayer";
import { PlayMark } from "../files/KindMarks";
import HeadDownload from "../files/HeadDownload";

// The player and its playlist load when a video or audio file opens.
const MediaPlaylist = lazy(() => import("../files/MediaPlaylist"));

// asLink reads a visitor link's answer: its address. The server's end time is not read: a player decides from what it
// sees, not from a clock (lib/files/media.ts).
function asLink(got: { url: string }): Link {
  return { url: got.url };
}

// The playlist a video or audio file opened: the file, and the folder's files read a page at a time.
interface MediaSeed {
  start: Node;
  folder: Pager;
}

// PublicBrowse shows what an anyone link opens: a folder, with its children and a path that stops at the link's root,
// or a file with a preview and a Download button. The bearer credential is held by the page and passed in; this
// component never stores it. A 401 means the credential ended, and a 404 means the link is dead: both are handed to
// the page, which decides what to do.
// A video or audio file plays in the same playlist the viewer uses (MediaPlaylist), over the folder the link reaches. Its
// files are read a page at a time under the visitor's credential, and no link is asked for a file until it plays.
export interface PublicBrowseProps {
  token: string;
  root: string;
  onlost: (why: "expired" | "gone") => void;
}

export default function PublicBrowse({ token, root, onlost }: PublicBrowseProps) {
  const { t } = useT();
  // The nodes the visitor has seen. A path is always made of these, since the visitor only moves down from the root,
  // so nothing above the root is ever asked for.
  const cache = useRef(new Map<string, Node>());
  const tokenRef = useRef(token);
  tokenRef.current = token;
  const onlostRef = useRef(onlost);
  onlostRef.current = onlost;
  const aliveRef = useRef(true);
  // listing is the folder the visitor last opened, with the children read and the page after them.
  const listing = useRef<{ id: string; items: Node[]; cursor: string | null } | null>(null);
  const [current, setCurrent] = useState<Node | null>(null);
  // toggleSlot is the head row's place for the playlist button while a media file plays (MediaPlaylist).
  const [toggleSlot, setToggleSlot] = useState<HTMLElement | null>(null);
  const [trail, setTrail] = useState<NodeRef[]>([]);
  const [children, setChildren] = useState<Node[]>([]);
  const [previewLink, setPreviewLink] = useState<Link | null>(null);
  const [media, setMedia] = useState<MediaSeed | null>(null);
  // mediaNow is the playlist's file on screen, the one its Download button saves. mediaDownload is the answer the server
  // gave for that file's attachment link, asked when the file starts (as main asked at open): allowed shows the button, a
  // refusal shows the downloads-off notice, and no answer yet shows neither.
  const [mediaNow, setMediaNow] = useState<Node | null>(null);
  const [mediaDownload, setMediaDownload] = useState<{ id: string; allowed: boolean } | null>(null);
  const [downloadURL, setDownloadURL] = useState<string | null>(null);
  const [downloadRestricted, setDownloadRestricted] = useState(false);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // lost turns a refused call into the page's answer: an ended credential, or a dead link. Anything else is shown as a
  // failure here, with the one generic message.
  const lost = (err: unknown): void => {
    if (err instanceof ApiError && err.status === 401) return onlostRef.current("expired");
    if (err instanceof ApiError && err.status === 404) return onlostRef.current("gone");
    setError(t("error.generic"));
  };

  // allChildren reads the folder's pages, up to a fixed number of them, and gives the page after the last one read.
  const allChildren = async (id: string): Promise<{ items: Node[]; cursor: string | null }> => {
    const out: Node[] = [];
    let cursor: string | undefined;
    for (let page = 0; page < 50; page++) {
      const res = await visitorChildren(tokenRef.current, id, cursor);
      out.push(...res.items);
      if (!res.next_cursor) return { items: out, cursor: null };
      cursor = res.next_cursor;
    }
    return { items: out, cursor: cursor ?? null };
  };

  // prepareFile asks for a preview link, when the type has one, and a download link, which the server refuses when
  // no-download applies. Neither is asked for again until the file is opened again.
  const prepareFile = async (node: Node): Promise<{ preview: Link | null; download: string | null; restricted: boolean }> => {
    let preview: Link | null = null;
    if (previewKind(node.mime_type) !== null) {
      try {
        preview = asLink(await visitorLink(tokenRef.current, node.id, "inline"));
      } catch (err) {
        if (err instanceof ApiError && (err.status === 401 || err.status === 404)) throw err;
        preview = null;
      }
    }
    try {
      const download = (await visitorLink(tokenRef.current, node.id, "attachment")).url;
      return { preview, download, restricted: false };
    } catch (err) {
      if (err instanceof ApiError && err.code === "download_restricted") return { preview, download: null, restricted: true };
      if (err instanceof ApiError && (err.status === 401 || err.status === 404)) throw err;
      return { preview, download: null, restricted: false };
    }
  };

  // shareGone is asked by the playlist when a file's link is refused (401 or 404). It asks the share's root once: only if the
  // share itself refuses (401 or 404) is the share gone, and the page then gives its answer. A file's refusal alone is
  // that file's failure, so the playlist moves on.
  const shareGone = async (err: unknown): Promise<boolean> => {
    if (!(err instanceof ApiError) || (err.status !== 401 && err.status !== 404)) return false;
    try {
      await visitorNode(tokenRef.current, root);
      return false;
    } catch (again) {
      if (again instanceof ApiError && (again.status === 401 || again.status === 404)) {
        lost(again);
        return true;
      }
      return false;
    }
  };

  // fetchLink asks for a link to a file the playlist plays: the visitor's preview link, one per file played.
  const fetchLink = (n: Node): Promise<Link> => visitorLink(tokenRef.current, n.id, "inline").then(asLink);

  // onMediaCurrent is told the file the playlist plays. Its download answer is asked for now, once per file, so the page shows
  // the button or the notice for that file and no other.
  const onMediaCurrent = (n: Node | null): void => {
    setMediaNow(n);
    if (n === null || n.kind === "dir") return;
    void visitorLink(tokenRef.current, n.id, "attachment").then(
      () => setMediaDownload({ id: n.id, allowed: true }),
      (err: unknown) => {
        if (err instanceof ApiError && err.code === "download_restricted") setMediaDownload({ id: n.id, allowed: false });
        else setMediaDownload(null);
      },
    );
  };

  // downloadNow starts a download of a file of the playlist. The link is asked for on the click, not before.
  const downloadNow = async (n: Node): Promise<void> => {
    try {
      const { url } = await visitorLink(tokenRef.current, n.id, "attachment");
      const a = document.createElement("a");
      a.href = url;
      a.download = "";
      a.click();
    } catch (err) {
      if (err instanceof ApiError && err.code === "download_restricted") setError(t("share.open.noDownload"));
      else lost(err);
    }
  };

  // Each move gets a number. Only the latest move may set the screen, so a slow answer to an earlier click cannot
  // replace the folder the visitor picked last.
  const moveRef = useRef(0);
  const show = async (id: string): Promise<void> => {
    const move = ++moveRef.current;
    const live = (): boolean => aliveRef.current && move === moveRef.current;
    setBusy(true);
    setError(null);
    try {
      const node = cache.current.get(id) ?? (await visitorNode(tokenRef.current, id));
      cache.current.set(id, node);
      if (!live()) return;
      setPreviewLink(null);
      setMedia(null);
      // The playlist's file belongs to the file being shown: a new file clears it at once, so no render names the last one.
      setMediaNow(null);
      setMediaDownload(null);
      setDownloadURL(null);
      setDownloadRestricted(false);
      if (node.kind === "dir") {
        const read = await allChildren(id);
        // A move that is no longer the latest must not change what the visitor's next file is seeded from, so the listing
        // is set only once the move is known to be current.
        if (!live()) return;
        for (const c of read.items) cache.current.set(c.id, c);
        listing.current = { id, items: read.items, cursor: read.cursor };
        setChildren(read.items);
      } else {
        setChildren([]);
        const type = kindOf(node);
        if (type === "video" || type === "audio") {
          // The playlist's folder is the one the visitor came from, when it is the one on screen; the file alone otherwise.
          const trailNow = crumbsTo(cache.current, id, root) ?? [];
          const parent = trailNow.length >= 2 ? trailNow[trailNow.length - 2].id : null;
          const seen = parent !== null && listing.current?.id === parent ? listing.current : null;
          const folder = new Pager(seen?.items ?? [], seen?.cursor ?? null, (c) => visitorChildren(tokenRef.current, parent ?? id, c));
          if (!live()) return;
          setMedia({ start: node, folder });
        } else {
          const got = await prepareFile(node);
          if (!live()) return;
          setPreviewLink(got.preview);
          setDownloadURL(got.download);
          setDownloadRestricted(got.restricted);
        }
      }
      if (!live()) return;
      setCurrent(node);
      setTrail(crumbsTo(cache.current, id, root) ?? []);
    } catch (err) {
      if (live()) lost(err);
    } finally {
      if (live()) setBusy(false);
    }
  };

  useEffect(() => {
    aliveRef.current = true;
    void show(root);
    return () => {
      aliveRef.current = false;
    };
    // The first show reads the root; later moves come from the user's clicks.
  }, []);

  const kind = current ? previewKind(current.mime_type) : null;
  const playing = current !== null && current.kind !== "dir" && media !== null;

  return (
    <div>
      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBottom: 12 }} />}

      {busy && current === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>
          {t("openlink.working")}
        </p>
      )}

      {current !== null && (
        <>
          {trail.length > 0 && (
            <nav className="crumbs" aria-label={t("share.open.path")} style={{ marginBottom: 8 }}>
              <ol style={{ display: "flex", flexWrap: "wrap", gap: 4, margin: 0, padding: 0, listStyle: "none" }}>
                {trail.map((crumb, i) => (
                  <li key={crumb.id}>
                    {i === trail.length - 1 ? (
                      <span aria-current="location">{crumb.name}</span>
                    ) : (
                      <Button type="link" style={{ padding: 0 }} onClick={() => void show(crumb.id)}>
                        {crumb.name}
                      </Button>
                    )}
                    {i < trail.length - 1 && <span aria-hidden="true"> / </span>}
                  </li>
                ))}
              </ol>
            </nav>
          )}

          {/* The heading names the file on screen: the playlist's file while one plays, the page's file otherwise. */}
          <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{(playing && mediaNow ? mediaNow : current).name}</h1>

          {current.kind === "dir" ? (
            children.length === 0 ? (
              <p style={{ color: "var(--ant-color-text-secondary)" }}>{t("share.open.empty")}</p>
            ) : (
              <ul className="rows" style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
                {children.map((child) => (
                  <li key={child.id} className="row" style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
                    <Button type="link" style={{ padding: 0 }} onClick={() => void show(child.id)}>
                      {child.name}
                    </Button>
                    <span style={{ color: "var(--ant-color-text-secondary)" }}>
                      {child.kind === "dir" ? t("share.open.folder") : t("share.open.file")}
                    </span>
                    {(kindOf(child) === "video" || kindOf(child) === "audio") && <PlayMark kind={kindOf(child) as "video" | "audio"} />}
                  </li>
                ))}
              </ul>
            )
          ) : playing && media ? (
            <>
              {/* The head row: the playlist button (drawn by MediaPlaylist into toggleSlot) sits beside Download, as in the viewer. */}
              <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", gap: 8, marginBottom: 8 }}>
                <span ref={setToggleSlot} className="media-toggle-slot" style={{ display: "contents" }} />
                {mediaNow && mediaDownload?.id === mediaNow.id && mediaDownload.allowed && (
                  <HeadDownload label={t("files.act.download")} onClick={() => void downloadNow(mediaNow)} />
                )}
              </div>
              {mediaNow && mediaDownload?.id === mediaNow.id && !mediaDownload.allowed && (
                <p style={{ fontSize: 14, color: "var(--ant-color-text-secondary)", marginBottom: 8 }}>{t("share.open.noDownload")}</p>
              )}
              {/* The player needs a definite height: its screen, cover and video fill it. A share page has no viewer: the player
                  takes the window below the page's head (the head row, the title and the page's bar), and 360 px at least. */}
              <div style={{ display: "flex", flexDirection: "column", height: "calc(100vh - 220px)", minHeight: 360 }}>
                <Suspense fallback={<p role="status">{t("files.preview.loading")}</p>}>
                  <MediaPlaylist
                    start={media.start}
                    folder={media.folder}
                    fetchLink={fetchLink}
                    onCurrent={onMediaCurrent}
                    onLost={shareGone}
                    toggleSlot={toggleSlot}
                  />
                </Suspense>
              </div>
            </>
          ) : (
            <>
              {previewLink !== null && kind === "image" && (
                <img className="public-preview" src={previewLink.url} alt={current.name} style={{ maxWidth: "100%" }} />
              )}
              {previewLink !== null && kind === "pdf" && (
                <iframe className="public-preview" title={current.name} src={previewLink.url} style={{ width: "100%", minHeight: 480, border: 0 }} />
              )}
              {(previewLink === null || kind === null) && (
                <p style={{ color: "var(--ant-color-text-secondary)" }}>{t("share.open.noPreview")}</p>
              )}

              {downloadURL !== null && (
                <p style={{ margin: "12px 0" }}>
                  <a className="btn primary" href={downloadURL} download>
                    {t("share.open.download")}
                  </a>
                </p>
              )}
              {downloadURL === null && downloadRestricted && (
                <p style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("share.open.noDownload")}</p>
              )}
            </>
          )}
        </>
      )}
    </div>
  );
}
