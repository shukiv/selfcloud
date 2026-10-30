// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Modal, Select, Skeleton, theme as antdTheme } from "antd";
import { useLang, useT, translatePlural } from "../lib/i18n";
import { useStore } from "../lib/store";
import { session } from "../lib/session";
import { queryKey, refreshTree } from "../lib/query";
import { folders, loadFolders } from "../lib/files/folders";
import { personLabel } from "../lib/files/people";
import { emptyTrashStep, getMe, listTrash, purgeTrashed, restoreNode, statOrNull } from "../lib/files/api";
import { fileErrorText } from "../lib/files/fileErrors";
import { daysUntil, formatDate } from "../lib/files/format";
import { atLeast } from "../lib/files/roles";
import { kindOf } from "../lib/files/kind";
import type { Page, TrashItem } from "../lib/files/types";
import Icon from "../components/files/Icon";

// Trash lists items deleted but not yet gone for good. Restore brings one back; deleting for good cannot be
// undone, so it asks first.
type Confirm = { kind: "purge"; item: TrashItem } | { kind: "empty" } | null;

export default function TrashPage() {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  const qc = useQueryClient();
  const allFolders = useStore(folders);
  const me = useStore(session);
  const [folder, setFolder] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [emptying, setEmptying] = useState<{ purged: number } | null>(null);

  const signedIn = me !== null;
  const meQ = useQuery({ queryKey: queryKey("me"), queryFn: () => getMe(), enabled: signedIn });
  const personalID = meQ.data?.personal_folder_id ?? null;
  useEffect(() => {
    if (signedIn) void loadFolders().catch(() => undefined);
  }, [signedIn]);

  const trashQ = useInfiniteQuery({
    queryKey: queryKey("trash", folder),
    enabled: signedIn,
    // A visit reads the trash again, so an item restored elsewhere is not shown as still here.
    refetchOnMount: "always",
    queryFn: ({ pageParam }: { pageParam: string | undefined }) => listTrash(folder || null, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<TrashItem>) => last.next_cursor,
  });
  const items = useMemo<TrashItem[]>(() => (trashQ.data?.pages ?? []).flatMap((p) => p.items), [trashQ.data]);

  // places names where each item was, for as far as the caller can reach. Where it cannot be reached, the
  // place is the space the item is in, or an unknown place.
  const parentIDs = useMemo(() => [...new Set(items.map((i) => i.original_parent_id))].sort(), [items]);
  // One cached lookup per place: when a page adds places, only those are looked up.
  const placeQs = useQueries({
    queries: parentIDs.map((id) => ({
      queryKey: queryKey("trash-place", id),
      enabled: signedIn,
      queryFn: async () => {
        const parent = await statOrNull(id);
        return parent ? (parent.parent_id === null ? "" : parent.name) : "";
      },
    })),
  });
  const places: Record<string, string> = {};
  parentIDs.forEach((id, i) => {
    const name = placeQs[i]?.data;
    if (name !== undefined) places[id] = name;
  });

  function placeOf(it: TrashItem): string {
    const name = places[it.original_parent_id];
    if (name === undefined) return "";
    if (name !== "") return name;
    const f = allFolders.find((x) => x.id === it.folder_id);
    return f ? f.name : t("trash.place.unknown");
  }


  // byLine names who trashed the item, or nothing when the caller cannot name them.
  function byLine(it: TrashItem): string {
    const who = personLabel(it.trashed_by, me, t);
    return who ? ` · ${t("trash.by", { who })}` : "";
  }

  function days(it: TrashItem): string {
    return translatePlural(lang, "trash.purge", daysUntil(it.purge_at));
  }

  // A restore or a purge changes the trash and the listings and places it touches.
  function refresh() {
    return refreshTree(qc);
  }

  async function restore(it: TrashItem) {
    setNotice(null);
    setError(null);
    try {
      const back = await restoreNode(it.id);
      setNotice(back.name !== it.name ? t("trash.restoredAs", { name: back.name }) : t("trash.restored", { name: it.name }));
      await refresh();
    } catch (err) {
      setError(fileErrorText(err, lang));
    }
  }

  async function purge(it: TrashItem) {
    setConfirm(null);
    try {
      await purgeTrashed(it.id);
      setNotice(t("trash.purged", { name: it.name }));
      await refresh();
    } catch (err) {
      setError(fileErrorText(err, lang));
    }
  }

  // emptyTrash purges in steps of up to 1000 items, as long as the server says more remain.
  async function emptyTrash() {
    setConfirm(null);
    setEmptying({ purged: 0 });
    setError(null);
    try {
      let purged = 0;
      for (;;) {
        const step = await emptyTrashStep(folder || null);
        purged += step.purged;
        setEmptying({ purged });
        if (!step.more) break;
      }
      setNotice(t("trash.emptied"));
      await refresh();
    } catch (err) {
      setError(fileErrorText(err, lang));
    } finally {
      setEmptying(null);
    }
  }

  useEffect(() => {
    document.title = `${t("trash.title")} · ${t("nav.trash")}`;
  }, [t]);

  const anyManager = items.some((i) => i.role === "manager");
  const loading = trashQ.isPending;

  return (
    <>
      <h1 style={{ fontSize: 24, margin: "0 0 16px" }}>{t("trash.title")}</h1>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 12, marginBottom: 16 }}>
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span>{t("trash.space")}</span>
          <Select
            aria-label={t("trash.space")}
            value={folder}
            onChange={(v: string) => setFolder(v)}
            style={{ minWidth: 180 }}
            options={[
              { value: "", label: t("trash.allSpaces") },
              ...allFolders.map((f) => ({
                value: f.id,
                label: f.kind === "personal" || f.id === personalID ? t("nav.files") : f.name,
              })),
            ]}
          />
        </label>
        {anyManager && (
          <Button danger onClick={() => setConfirm({ kind: "empty" })} disabled={emptying !== null}>
            {t("trash.empty")}
          </Button>
        )}
      </div>

      {emptying && (
        <Alert type="info" showIcon role="status" message={t("trash.emptying", { count: emptying.purged })} style={{ marginBottom: 12 }} />
      )}
      {notice && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {error && <Alert type="error" showIcon role="alert" message={error} style={{ marginBottom: 12 }} />}

      {loading ? (
        <div role="status" aria-busy="true">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      ) : trashQ.isError ? (
        <Alert type="error" showIcon role="alert" message={fileErrorText(trashQ.error, lang)} />
      ) : items.length === 0 ? (
        <p role="status" style={{ padding: "32px 16px", textAlign: "center", border: `1px dashed ${token.colorBorder}`, borderRadius: token.borderRadiusLG }}>
          {t("trash.empty.none")}
        </p>
      ) : (
        <ul aria-label={t("trash.title")} style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
          {items.map((it) => (
            <li
              key={it.id}
              className="trash-item"
              style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 12, padding: 12, border: `1px solid ${token.colorBorderSecondary}`, borderRadius: 4 }}
            >
              <Icon kind={kindOf(it)} size={28} />
              <div style={{ flex: "1 1 200px", minWidth: 0 }}>
                <p className="name" style={{ margin: 0, fontWeight: 600, overflowWrap: "anywhere" }}>
                  <bdi>{it.name}</bdi>
                </p>
                <p style={{ margin: 0, fontSize: 14, opacity: 0.75 }}>
                  {placeOf(it) && `${t("trash.was", { place: placeOf(it) })} · `}
                  {t("trash.trashedOn", { date: formatDate(it.trashed_at, lang) })}
                  {byLine(it)}
                </p>
                <p style={{ margin: 0, fontSize: 14, opacity: 0.75 }}>{days(it)}</p>
              </div>
              <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                {it.role && atLeast(it.role, "editor") && <Button onClick={() => void restore(it)}>{t("trash.restore")}</Button>}
                {it.role === "manager" && (
                  <Button danger onClick={() => setConfirm({ kind: "purge", item: it })}>
                    {t("trash.purgeNow")}
                  </Button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {trashQ.hasNextPage && (
        <div style={{ display: "flex", justifyContent: "center", marginTop: 16 }}>
          <Button onClick={() => void trashQ.fetchNextPage()} disabled={trashQ.isFetchingNextPage}>
            {t("files.loadMore")}
          </Button>
        </div>
      )}

      <Modal
        open={confirm !== null}
        title={confirm?.kind === "empty" ? t("trash.empty.title") : t("trash.purge.title")}
        okText={confirm?.kind === "empty" ? t("trash.empty.submit") : t("trash.purge.submit")}
        okButtonProps={{ danger: true }}
        cancelText={t("common.cancel")}
        onCancel={() => setConfirm(null)}
        maskClosable={false}
        onOk={() => {
          if (confirm?.kind === "empty") void emptyTrash();
          else if (confirm?.kind === "purge") void purge(confirm.item);
        }}
      >
        <p>{confirm?.kind === "empty" ? t("trash.empty.body") : t("trash.purge.body")}</p>
      </Modal>
    </>
  );
}

