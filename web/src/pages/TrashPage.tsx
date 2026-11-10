// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Modal, Select, Skeleton } from "antd";
import { ArchiveRestore, Trash2 } from "lucide-react";
import IconButton from "../components/IconButton";
import Listing, { type ListColumn } from "../components/files/Listing";
import Icon from "../components/files/Icon";
import KindMarks from "../components/files/KindMarks";
import { useLang, useT, translatePlural } from "../lib/i18n";
import { useStore } from "../lib/store";
import { session } from "../lib/session";
import { queryKey, refreshTree } from "../lib/query";
import { refreshMyStorage } from "../lib/storage/api";
import { folders, loadFolders } from "../lib/files/folders";
import { emptyTrashStep, getMe, listTrash, purgeTrashed, restoreNode } from "../lib/files/api";
import { fileErrorText } from "../lib/files/fileErrors";
import { daysUntil, formatBytes, formatDate } from "../lib/files/format";
import { atLeast } from "../lib/files/roles";
import { kindOf } from "../lib/files/kind";
import type { Page, TrashItem } from "../lib/files/types";
import { useDriveText } from "../lib/drive/driveText";
import { locationOf } from "../lib/drive/location";

// Trash lists items deleted but not yet gone for good, on the one table (tl4, decision 137). Restore brings items back, in one
// action or one at a time; deleting for good cannot be undone, so it asks first, and it is for a manager of the item only.
// Empty trash is an icon with a name, and it asks first too.
type Confirm = { kind: "purge"; items: TrashItem[] } | { kind: "empty" } | null;

const canRestore = (it: TrashItem) => it.role !== undefined && atLeast(it.role, "editor");
const canPurge = (it: TrashItem) => it.role === "manager";

export default function TrashPage() {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const allFolders = useStore(folders);
  const me = useStore(session);
  const driveReady = useDriveText();
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
    // A space picked again is read again, even inside the window in which a read is fresh (fu8, decision 94).
    staleTime: 0,
    queryFn: ({ pageParam }: { pageParam: string | undefined }) => listTrash(folder || null, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<TrashItem>) => last.next_cursor,
  });
  const items = useMemo<TrashItem[]>(() => (trashQ.data?.pages ?? []).flatMap((p) => p.items), [trashQ.data]);

  // A restore or a purge changes the trash and the listings and places it touches, and the bytes the sidebar's meter shows.
  function refresh() {
    void refreshMyStorage().catch(() => undefined);
    return refreshTree(qc);
  }

  // restoreMany brings the given items back, one after another; a failure shows its error and stops there.
  async function restoreMany(list: TrashItem[]) {
    setNotice(null);
    setError(null);
    const names: string[] = [];
    try {
      for (const it of list) {
        const back = await restoreNode(it.id);
        names.push(back.name !== it.name ? t("trash.restoredAs", { name: back.name }) : t("trash.restored", { name: it.name }));
      }
      if (names.length > 0) setNotice(names.join(" "));
      await refresh();
    } catch (err) {
      setError(fileErrorText(err, lang));
      await refresh();
    }
  }

  // purgeMany deletes the given items for good, one after another, after the person has confirmed.
  async function purgeMany(list: TrashItem[]) {
    setConfirm(null);
    setNotice(null);
    try {
      for (const it of list) {
        await purgeTrashed(it.id);
        setNotice(t("trash.purged", { name: it.name }));
      }
      await refresh();
    } catch (err) {
      setError(fileErrorText(err, lang));
      await refresh();
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

  const anyManager = items.some(canPurge);
  const loading = trashQ.isPending;

  function days(it: TrashItem): string {
    return translatePlural(lang, "trash.purge", daysUntil(it.purge_at));
  }

  const columns: ListColumn<TrashItem>[] = [
    {
      key: "name",
      title: t("files.col.name"),
      render: (it) => (
        <span className="name" style={{ display: "block", minWidth: 0, overflowWrap: "anywhere", fontWeight: 600 }}>
          <bdi>{it.name}</bdi>
          <KindMarks node={it} />
        </span>
      ),
    },
    {
      key: "where",
      title: t("drive.col.originalLocation"),
      secondary: true,
      render: (it) => locationOf({ place: it.place, folder_id: it.folder_id, parent_id: it.original_parent_id }, allFolders, t("nav.files")),
    },
    {
      key: "deleted",
      title: t("drive.col.deletedOn"),
      secondary: true,
      render: (it) => (
        <span style={{ display: "block" }}>
          <time dateTime={it.trashed_at}>{formatDate(it.trashed_at, lang)}</time>
          <span style={{ display: "block", fontSize: 14, opacity: 0.75 }}>{days(it)}</span>
        </span>
      ),
    },
    { key: "size", title: t("files.col.size"), secondary: true, align: "end", render: (it) => (it.kind === "dir" ? "—" : formatBytes(it.size, lang)) },
  ];

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
          <IconButton
            label={t("trash.empty")}
            danger
            disabled={emptying !== null}
            onClick={() => setConfirm({ kind: "empty" })}
            icon={<Trash2 size={18} aria-hidden="true" focusable="false" />}
          />
        )}
      </div>

      {emptying && (
        <Alert type="info" showIcon role="status" message={t("trash.emptying", { count: emptying.purged })} style={{ marginBottom: 12 }} />
      )}
      {notice && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {error && <Alert type="error" showIcon role="alert" message={error} style={{ marginBottom: 12 }} />}

      {loading || !driveReady ? (
        <div role="status" aria-busy="true">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      ) : trashQ.isError ? (
        <Alert type="error" showIcon role="alert" message={fileErrorText(trashQ.error, lang)} />
      ) : (
        <div className="items">
          <Listing<TrashItem>
            label={t("trash.title")}
            items={items}
            rowLabel={(it) => it.name}
            rowClassName={() => "trash-item"}
            columns={columns}
            icon={(it) => <Icon kind={kindOf(it)} size={28} node={it} />}
            selectable
            bulk={(ids) => {
              const picked = items.filter((it) => ids.includes(it.id));
              return [
                {
                  key: "restore",
                  label: t("trash.restore"),
                  icon: <ArchiveRestore size={18} aria-hidden="true" focusable="false" />,
                  enabled: picked.some(canRestore),
                  run: () => void restoreMany(picked.filter(canRestore)),
                },
                {
                  key: "purge",
                  label: t("trash.purgeNow"),
                  icon: <Trash2 size={18} aria-hidden="true" focusable="false" />,
                  enabled: picked.some(canPurge),
                  run: () => setConfirm({ kind: "purge", items: picked.filter(canPurge) }),
                },
              ];
            }}
            actions={(it) => [
              ...(canRestore(it)
                ? [
                    {
                      key: "restore",
                      label: `${t("trash.restore")}: ${it.name}`,
                      icon: <ArchiveRestore size={18} aria-hidden="true" focusable="false" />,
                      run: () => void restoreMany([it]),
                    },
                  ]
                : []),
              ...(canPurge(it)
                ? [
                    {
                      key: "purge",
                      label: `${t("trash.purgeNow")}: ${it.name}`,
                      icon: <Trash2 size={18} aria-hidden="true" focusable="false" />,
                      danger: true,
                      run: () => setConfirm({ kind: "purge", items: [it] }),
                    },
                  ]
                : []),
            ]}
            empty={{ title: t("drive.empty.trash"), className: "empty" }}
          />
        </div>
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
          else if (confirm?.kind === "purge") void purgeMany(confirm.items);
        }}
      >
        <p>{confirm?.kind === "empty" ? t("trash.empty.body") : t("trash.purge.body")}</p>
      </Modal>
    </>
  );
}
