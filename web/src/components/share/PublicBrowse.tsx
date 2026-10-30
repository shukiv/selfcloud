// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Alert, Button } from "antd";
import { useT } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import type { Node } from "../../lib/files/types";
import { visitorChildren, visitorLink, visitorNode } from "../../lib/sharing/api";
import { crumbsTo, previewKind, type NodeRef } from "../../lib/sharing/anyone";

// PublicBrowse shows what an anyone link opens: a folder, with its children and a path that stops at the link's root,
// or a file with a preview and a Download button. The bearer credential is held by the page and passed in; this
// component never stores it. A 401 means the credential ended, and a 404 means the link is dead: both are handed to
// the page, which decides what to do.
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
  const [current, setCurrent] = useState<Node | null>(null);
  const [trail, setTrail] = useState<NodeRef[]>([]);
  const [children, setChildren] = useState<Node[]>([]);
  const [previewURL, setPreviewURL] = useState<string | null>(null);
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

  // allChildren reads every page of a folder, up to a fixed number of pages.
  const allChildren = async (id: string): Promise<Node[]> => {
    const out: Node[] = [];
    let cursor: string | undefined;
    for (let page = 0; page < 50; page++) {
      const res = await visitorChildren(tokenRef.current, id, cursor);
      out.push(...res.items);
      if (!res.next_cursor) break;
      cursor = res.next_cursor;
    }
    return out;
  };

  // prepareFile asks for a preview link, when the type has one, and a download link, which the server refuses when
  // no-download applies. Neither is asked for again until the file is opened again.
  const prepareFile = async (node: Node): Promise<{ preview: string | null; download: string | null; restricted: boolean }> => {
    let preview: string | null = null;
    if (previewKind(node.mime_type) !== null) {
      try {
        preview = (await visitorLink(tokenRef.current, node.id, "inline")).url;
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
      setPreviewURL(null);
      setDownloadURL(null);
      setDownloadRestricted(false);
      if (node.kind === "dir") {
        const kids = await allChildren(id);
        for (const c of kids) cache.current.set(c.id, c);
        if (!live()) return;
        setChildren(kids);
      } else {
        setChildren([]);
        const got = await prepareFile(node);
        if (!live()) return;
        setPreviewURL(got.preview);
        setDownloadURL(got.download);
        setDownloadRestricted(got.restricted);
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

          <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{current.name}</h1>

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
                  </li>
                ))}
              </ul>
            )
          ) : (
            <>
              {previewURL !== null && kind === "image" && (
                <img className="public-preview" src={previewURL} alt={current.name} style={{ maxWidth: "100%" }} />
              )}
              {previewURL !== null && kind === "video" && (
                <video className="public-preview" controls src={previewURL} aria-label={current.name} style={{ maxWidth: "100%" }} />
              )}
              {previewURL !== null && kind === "audio" && <audio controls src={previewURL} />}
              {previewURL !== null && kind === "pdf" && (
                <iframe className="public-preview" title={current.name} src={previewURL} style={{ width: "100%", minHeight: 480, border: 0 }} />
              )}
              {(previewURL === null || kind === null) && (
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
