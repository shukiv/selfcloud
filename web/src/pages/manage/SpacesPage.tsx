// SPDX-License-Identifier: AGPL-3.0-or-later
import { Pencil, Settings, Share2, Trash2 } from "lucide-react";

import { useState } from "react";
import { App, Alert, Button, Tag } from "antd";
import { useFlight } from "../../lib/flight";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { statNode } from "../../lib/files/api";
import type { FolderAccess, Node } from "../../lib/files/types";
import { createSpace, deleteSpace, getSpace, listSpaces, renameSpace } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { useAdminText } from "../../lib/manage/adminText";
import type { Space } from "../../lib/manage/types";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import Listing, { type ListColumn } from "../../components/files/Listing";
import ShareDialog from "../../components/share/ShareDialog";
import SpaceSettings from "../../components/share/SpaceSettings";
import Confirm from "../../components/Confirm";
import NameDialog from "../../components/manage/NameDialog";
import { OwnerOnly, useAccountID, useManageTitle, useOncePer } from "../../components/manage/OwnerPage";
import { LOCK } from "../../lib/manage/locks";

// Spaces: the team spaces of the account. Sharing a space goes through the share dialog on its root, the same one the
// file browser uses. Deleting a space removes it and everything in it for everyone.

// asAccess gives a space the shape the share dialog and the settings dialog read. An owner manages every space, so the
// role is manager; the server still checks each change.
function asAccess(s: Space): FolderAccess {
  return {
    id: s.id,
    name: s.name,
    kind: "team",
    access: "write",
    role: "manager",
    hidden: false,
    members_only: s.members_only,
    sharing_managers_only: s.sharing_managers_only,
    no_download_for_viewers: s.no_download_for_viewers,
    allow_links: s.allow_links,
  };
}

interface Deleting {
  space: Space;
  files: number;
  bytes: number;
}

function SpacesView() {
  const { t, tp } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  useManageTitle("manage.spaces.title");
  const [notice, setNotice] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState<Space | null>(null);
  const [deleting, setDeleting] = useState<Deleting | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const flight = useFlight();
  const { message } = App.useApp();
  const adminText = useAdminText();
  // cancelDelete closes the dialog at once, busy or not: the delete on its way is aborted, and the spaces are read again (fu4).
  const cancelDelete = () => {
    if (flight.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      void refresh();
    }
    setDeleteError(null);
    setDeleting(null);
  };
  const [sharing, setSharing] = useState<{ node: Node; space: FolderAccess } | null>(null);
  const [settings, setSettings] = useState<FolderAccess | null>(null);
  const once = useOncePer();

  const q = useInfiniteQuery({
    queryKey: queryKey("manage", account, "spaces"),
    queryFn: ({ pageParam }) => listSpaces(account, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const spaces: Space[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: queryKey("manage", account, "spaces") });

  // openShare reads the space's root, which the share dialog works on.
  const openShare = (s: Space) =>
    once(LOCK.spaceShare(s.id), async () => {
      const stale = sessionChanged();
      setActionError(null);
      try {
        const node = await statNode(s.root_node_id);
        if (stale()) return;
        setSharing({ node, space: asAccess(s) });
      } catch (err) {
        if (stale()) return;
        setActionError(failureText(err, lang));
      }
    });

  // openDelete reads the space's totals first, so the confirmation can say what goes.
  const openDelete = (s: Space) =>
    once(LOCK.spaceOpenDelete(s.id), async () => {
      const stale = sessionChanged();
      setActionError(null);
      try {
        const d = await getSpace(account, s.id);
        if (stale()) return;
        setDeleteError(null);
        setDeleting({ space: s, files: d.files, bytes: d.bytes });
      } catch (err) {
        if (stale()) return;
        setActionError(failureText(err, lang));
      }
    });

  const confirmDelete = () =>
    once(LOCK.spaceDelete(deleting?.space.id ?? ""), async () => {
      if (!deleting) return;
      const stale = sessionChanged();
      const target = deleting.space;
      const signal = flight.start();
      setDeleteBusy(true);
      setDeleteError(null);
      try {
        await deleteSpace(account, target.id, signal);
        if (stale()) return;
        setNotice(t("spaces.deleted", { name: target.name }));
        setDeleting(null);
        await refresh();
      } catch (err) {
        if (stale() || signal.aborted) return;
        setDeleteError(failureText(err, lang));
      } finally {
        flight.end();
        setDeleteBusy(false);
      }
    });

  const failure = q.isError ? failureText(q.error, lang) : null;
  const loaded = q.data !== undefined;

  // The flags a space has, as Tags in its Access column (design-tl 3.2, M4); on a phone they are the name's second line.
  const flagsOf = (s: Space): string[] =>
    [
      s.members_only ? t("spaces.flag.membersOnly") : "",
      s.sharing_managers_only ? t("spaces.flag.managersOnly") : "",
      s.no_download_for_viewers ? t("spaces.flag.noDownload") : "",
    ].filter(Boolean);
  const columns: ListColumn<Space>[] = [
    { key: "name", title: t("files.col.name"), render: (s) => <bdi>{s.name}</bdi> },
    {
      key: "access",
      title: t("admin.table.access"),
      secondary: true,
      render: (s) => (
        <>
          {flagsOf(s).map((f) => (
            <Tag key={f}>{f}</Tag>
          ))}
        </>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {t("manage.spaces.title")}
        </h1>
        <Button type="primary" onClick={() => setCreating(true)}>
          {t("spaces.new")}
        </Button>
      </div>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {actionError !== null && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBottom: 12 }} />}
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {!loaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {loaded && (
        <Listing
          label={t("manage.spaces.title")}
          items={spaces}
          rowLabel={(s) => s.name}
          columns={columns}
          actions={(s) => [
            {
              key: "share",
              label: `${t("spaces.share")}: ${s.name}`,
              icon: <Share2 size={18} aria-hidden="true" focusable="false" />,
              run: () => void openShare(s),
            },
            {
              key: "settings",
              label: `${t("spaces.settings")}: ${s.name}`,
              icon: <Settings size={18} aria-hidden="true" focusable="false" />,
              run: () => setSettings(asAccess(s)),
            },
            {
              key: "rename",
              label: `${t("spaces.rename")}: ${s.name}`,
              icon: <Pencil size={18} aria-hidden="true" focusable="false" />,
              run: () => setRenaming(s),
            },
            {
              key: "delete",
              label: `${t("spaces.delete")}: ${s.name}`,
              icon: <Trash2 size={18} aria-hidden="true" focusable="false" />,
              danger: true,
              run: () => void openDelete(s),
            },
          ]}
          empty={{
            title: t("spaces.empty"),
            description: adminText ? t("admin.empty.spaces") : undefined,
            action: { label: t("spaces.new"), onClick: () => setCreating(true) },
          }}
        />
      )}

      {q.hasNextPage && (
        <div style={{ display: "flex", justifyContent: "center", marginBlockStart: 16 }}>
          <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}

      {creating && (
        <NameDialog
          id="space-new"
          title={t("spaces.new")}
          label={t("spaces.nameLabel")}
          action={t("spaces.create")}
          onclose={() => setCreating(false)}
          onsave={async (name, signal) => {
            const stale = sessionChanged();
            await createSpace(account, name, signal);
            setCreating(false);
            if (stale()) return;
            setNotice(t("spaces.created", { name }));
            await refresh();
          }}
        />
      )}

      {renaming && (
        <NameDialog
          id="space-rename"
          title={t("spaces.rename")}
          label={t("spaces.nameLabel")}
          initial={renaming.name}
          action={t("spaces.save")}
          onclose={() => setRenaming(null)}
          onsave={async (name, signal) => {
            const stale = sessionChanged();
            const s = renaming;
            await renameSpace(account, s.id, name, signal);
            setRenaming(null);
            if (stale()) return;
            await refresh();
          }}
        />
      )}

      {deleting && (
        <Confirm
          id="space-delete"
          title={t("spaces.deleteTitle", { name: deleting.space.name })}
          message={`${tp("spaces.deleteFiles", deleting.files)} ${t("spaces.deleteSize", { size: formatBytes(deleting.bytes, lang) })} ${t("spaces.deleteEveryone")}`}
          action={t("spaces.deleteAction")}
          danger
          requireName={deleting.space.name}
          busy={deleteBusy}
          error={deleteError}
          onconfirm={() => void confirmDelete()}
          onclose={() => setDeleting(null)}
          oncancel={cancelDelete}
        />
      )}

      {sharing && <ShareDialog node={sharing.node} space={sharing.space} onclose={() => setSharing(null)} onchanged={() => undefined} />}

      {settings && (
        <SpaceSettings
          space={settings}
          onclose={() => setSettings(null)}
          onsaved={() => void refresh()}
        />
      )}
    </div>
  );
}

export default function SpacesPage() {
  return (
    <OwnerOnly section="spaces">
      <SpacesView />
    </OwnerOnly>
  );
}
