// SPDX-License-Identifier: AGPL-3.0-or-later

// useBookmarks holds the signed-in person's bookmarks for the file that plays (decision 218). It reads the list when the file
// changes, drops an answer for a file no longer current, and keeps decision 222's rule: a session that has changed sends nothing
// and uses no answer. add posts the rounded place with an empty label and inserts the answer in order; the cap and a failure each
// say their own words. The dialog is lazy, so Listing and the dialog never load with the player's first paint. A share link and an
// impersonated session are given no api, so they get no marks and no dialog.
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { App } from "antd";
import { ApiError } from "../../lib/api";
import { sessionChanged } from "../../lib/session";
import { useMT } from "../../lib/media/i18n";
import { formatTime } from "../../lib/files/mediaKeys";
import { BOOKMARK_LIMIT, sortBookmarks, type BookmarkApi, type Marks } from "../../lib/files/bookmarks";
import type { Bookmark } from "../../lib/files/api";

const BookmarksDialog = lazy(() => import("./BookmarksDialog"));

// ListState is one file's read list, keyed by its node so a previous file's list never shows on another.
interface ListState {
  nod: string;
  items: Bookmark[];
  ready: boolean;
  failed: boolean;
}

export function useBookmarks(
  api: BookmarkApi | undefined,
  nodeId: string | null,
  media: HTMLMediaElement | null,
): { marks?: Marks; dialog: ReactNode } {
  const mt = useMT();
  const { message } = App.useApp();
  // changed reports whether the session has ended or changed since this hook mounted (decision 222).
  const changed = useRef(sessionChanged()).current;
  const nodRef = useRef(nodeId);
  nodRef.current = nodeId;
  const [state, setState] = useState<ListState | null>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!api || !nodeId || changed()) return;
    const nod = nodeId;
    let live = true;
    // The read is deferred so a route that refuses at once (no session left) is a failed read, not a thrown effect.
    Promise.resolve()
      .then(() => api.list(nod))
      .then(
        (items) => {
          if (live && !changed()) setState({ nod, items: sortBookmarks(items), ready: true, failed: false });
        },
        () => {
          if (live && !changed()) setState({ nod, items: [], ready: false, failed: true });
        },
      );
    return () => {
      live = false;
    };
  }, [api, nodeId, changed]);

  const cur = state && state.nod === nodeId ? state : null;
  const items = cur?.items ?? [];
  const ready = cur?.ready ?? false;
  const readFailed = cur?.failed ?? false;
  const full = items.length >= BOOKMARK_LIMIT;

  const add = useCallback(() => {
    if (!api || !nodeId || !media || changed() || full) return;
    const nod = nodeId;
    const position = Math.round(media.currentTime * 10) / 10;
    api.add(nod, position, "").then(
      (bm) => {
        if (changed() || nodRef.current !== nod) return;
        setState((s) => (s && s.nod === nod ? { ...s, items: sortBookmarks([...s.items, bm]) } : s));
        message.success(mt.t("media.bookmarks.added", { time: formatTime(position) }));
      },
      (e: unknown) => {
        if (changed() || nodRef.current !== nod) return;
        if (e instanceof ApiError && e.status === 409 && e.details.reason === "limit") {
          const limit = typeof e.details.limit === "number" ? e.details.limit : BOOKMARK_LIMIT;
          message.error(mt.t("media.bookmarks.limit", { limit }));
        } else {
          message.error(mt.t("media.bookmarks.saveFailed"));
        }
      },
    );
  }, [api, nodeId, media, changed, full, message, mt]);

  const openList = useCallback(() => setOpen(true), []);
  const closeList = useCallback(() => setOpen(false), []);

  const relabel = useCallback(
    async (bmk: string, label: string): Promise<Bookmark> => {
      if (!api || !nodeId || changed()) throw new ApiError(0, "signed_out", {}, 0);
      const nod = nodeId;
      const updated = await api.relabel(nod, bmk, label);
      if (!changed() && nodRef.current === nod) {
        setState((s) => (s && s.nod === nod ? { ...s, items: sortBookmarks(s.items.map((x) => (x.id === bmk ? updated : x))) } : s));
      }
      return updated;
    },
    [api, nodeId, changed],
  );

  const remove = useCallback(
    async (bmk: string): Promise<void> => {
      if (!api || !nodeId || changed()) throw new ApiError(0, "signed_out", {}, 0);
      const nod = nodeId;
      await api.remove(nod, bmk);
      if (!changed() && nodRef.current === nod) {
        setState((s) => (s && s.nod === nod ? { ...s, items: s.items.filter((x) => x.id !== bmk) } : s));
      }
    },
    [api, nodeId, changed],
  );

  const retry = useCallback(() => {
    if (!api || !nodeId || changed()) return;
    const nod = nodeId;
    Promise.resolve()
      .then(() => api.list(nod))
      .then(
        (list) => {
          if (!changed() && nodRef.current === nod) setState({ nod, items: sortBookmarks(list), ready: true, failed: false });
        },
        () => {
          if (!changed() && nodRef.current === nod) setState({ nod, items: [], ready: false, failed: true });
        },
      );
  }, [api, nodeId, changed]);

  const marks: Marks | undefined = api ? { items, ready, full, add, openList } : undefined;

  // The dialog names the file from the element's own label, which the player sets to the file's name.
  const name = media?.getAttribute("aria-label") ?? "";
  const dialog =
    marks && open ? (
      <Suspense fallback={null}>
        <BookmarksDialog
          name={name}
          marks={marks}
          media={media}
          readFailed={readFailed}
          onRelabel={relabel}
          onRemove={remove}
          onRetry={retry}
          onClose={closeList}
        />
      </Suspense>
    ) : null;

  return { marks, dialog };
}
