// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Skeleton } from "antd";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { isAdmin, session, sessionChanged, sessionKey, signingOut } from "../lib/session";
import { fileErrorText } from "../lib/files/fileErrors";
import { deleteNode, restoreNode } from "../lib/files/api";
import { myBreakdown, myLargest, myStorage, refreshMyBreakdown, type LargestFile } from "../lib/storage/api";
import { LARGEST_PAGE, mergePage } from "../lib/storage/logic";
import { kindOfChip, type Breakdown, type ChipId } from "../lib/storage/breakdown";
import StorageMeter from "../components/storage/StorageMeter";
import FreeUpSpace from "../components/storage/FreeUpSpace";
import LargestFilesCard from "../components/storage/LargestFiles";
import EmptyState from "../components/EmptyState";

// A server that does not send the breakdown shows the figures with the files only.
const NO_BREAKDOWN: Breakdown = { versions_bytes: 0, versions_files: 0, trash_bytes: 0, trash_files: 0 };

// Storage: the caller's own My files, what it holds against its limit, the old versions and the trash, and the largest files
// in it, filtered by kind (decision 79). Team spaces are not here; they count against the account, and the owner sees them
// in Manage > Storage. An installation admin has no drive, so this page reads nothing for it. A failed first load shows its
// error and no empty state.
export default function StoragePage() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const my = useStore(myStorage);
  const breakdown = useStore(myBreakdown);
  const signedIn = me !== null;
  const admin = me !== null && isAdmin(me.user);
  const sk = sessionKey(me);
  const [files, setFiles] = useState<LargestFile[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [chip, setChip] = useState<ChipId>("all");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // figuresError is a failed read of the storage figures: the meter is not drawn then, and the error stands in its place.
  const [figuresError, setFiguresError] = useState<string | null>(null);
  // The language is read through a ref: a change of language must not clear the list and read it again.
  const langRef = useRef(lang);
  langRef.current = lang;
  // The chip in use is read through a ref, so a page read that is in flight keeps the kind it was asked for.
  const chipRef = useRef(chip);
  chipRef.current = chip;

  // latestLoad numbers the list's requests. Only the newest one may end the loading or show an error: an answer to an older
  // request (the previous chip's, say) must not clear the loading of the one still in flight.
  const latestLoad = useRef(0);

  const load = useCallback(async (reset: boolean, from: string | null, kind: ChipId) => {
    const changed = sessionChanged();
    const request = ++latestLoad.current;
    setLoading(true);
    try {
      const page = await myLargest(LARGEST_PAGE, reset ? null : from, kindOfChip(kind));
      if (changed() || chipRef.current !== kind || request !== latestLoad.current) return;
      setFiles((prev) => (reset ? page.items : mergePage(prev, page.items)));
      setCursor(page.next_cursor ?? null);
      setError(null);
    } catch (err) {
      if (!changed() && !signingOut() && request === latestLoad.current) setError(fileErrorText(err, langRef.current));
    } finally {
      if (request === latestLoad.current) setLoading(false);
    }
  }, []);

  // focusAfter is the row an action leaves the focus on, once the list is read again: the row that takes the removed
  // row's place (or the one before it, when the last row went), or the list's heading when no row is left (decision 79).
  const focusAfter = useRef<number | null>(null);
  useEffect(() => {
    if (loading || focusAfter.current === null) return;
    const index = focusAfter.current;
    focusAfter.current = null;
    if (files.length === 0) {
      document.getElementById("largest-title")?.focus();
      return;
    }
    const row = Math.min(index, files.length - 1);
    document.querySelector<HTMLElement>(`tr[data-row-index="${row}"] [data-first-action]`)?.focus();
  }, [files, loading]);

  // reloadAll reads the figures and the first page again, after a file moved to or out of the trash.
  const reloadAll = useCallback(async () => {
    const changed = sessionChanged();
    try {
      await refreshMyBreakdown();
      setFiguresError(null);
    } catch (err) {
      if (!changed()) setFiguresError(fileErrorText(err, langRef.current));
    }
    if (!changed()) await load(true, null, chipRef.current);
  }, [load]);

  useEffect(() => {
    document.title = `${t("storage.title")} · ${brandName}`;
  }, [t, brandName]);

  // Each mount reads the figures and then the largest files, for the person who is signed in.
  useEffect(() => {
    if (!signedIn || admin) return;
    setFiles([]);
    setCursor(null);
    setError(null);
    setFiguresError(null);
    setChip("all");
    chipRef.current = "all";
    const changed = sessionChanged();
    void (async () => {
      try {
        await refreshMyBreakdown();
      } catch (err) {
        if (!changed()) setFiguresError(fileErrorText(err, langRef.current));
      }
      if (changed()) return;
      await load(true, null, "all");
    })();
  }, [sk, signedIn, admin, load]);

  const pickChip = (id: ChipId) => {
    if (id === chipRef.current) return;
    setChip(id);
    chipRef.current = id;
    setFiles([]);
    setCursor(null);
    void load(true, null, id);
  };

  const trash = async (id: string) => {
    focusAfter.current = files.findIndex((f) => f.id === id);
    try {
      await deleteNode(id);
    } catch (err) {
      focusAfter.current = null;
      setError(fileErrorText(err, langRef.current));
      return;
    }
    await reloadAll();
  };

  const restore = async (id: string) => {
    focusAfter.current = files.findIndex((f) => f.id === id);
    try {
      await restoreNode(id);
    } catch (err) {
      focusAfter.current = null;
      setError(fileErrorText(err, langRef.current));
      return;
    }
    await reloadAll();
  };

  if (admin) return null;

  const empty = !loading && files.length === 0 && error === null;
  const figures = my;
  const parts = breakdown ?? NO_BREAKDOWN;

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("storage.title")}</h1>

      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}
      {figuresError !== null && <Alert type="error" showIcon role="alert" message={figuresError} style={{ marginBlockEnd: 12 }} />}

      {figures && figuresError === null && (
        <div style={{ display: "grid", gap: 20, marginBlockEnd: 20 }}>
          <StorageMeter figures={figures} breakdown={parts} />
          <FreeUpSpace breakdown={parts} />
        </div>
      )}

      <LargestFilesCard
        files={files}
        chip={chip}
        onChip={pickChip}
        hasMore={cursor !== null}
        loading={loading}
        onMore={() => void load(false, cursor, chipRef.current)}
        onTrash={(id) => void trash(id)}
        onRestore={(id) => void restore(id)}
      />
      {loading && files.length === 0 && error === null && <Skeleton active style={{ marginBlockStart: 12 }} />}
      {empty && <EmptyState role="status" className="empty" size="compact" text={t("storage.largestEmpty")} />}
    </div>
  );
}
