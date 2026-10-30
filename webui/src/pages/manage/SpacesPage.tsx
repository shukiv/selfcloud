// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button } from "antd";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { statNode } from "../../lib/files/api";
import type { FolderAccess, Node } from "../../lib/files/types";
import { createSpace, deleteSpace, getSpace, listSpaces, renameSpace } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import type { Space } from "../../lib/manage/types";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import ShareDialog from "../../components/share/ShareDialog";
import SpaceSettings from "../../components/share/SpaceSettings";
import Confirm from "../../components/Confirm";
import NameDialog from "../../components/manage/NameDialog";
import { OwnerOnly, useAccountID, useManageTitle, useOnce } from "../../components/manage/OwnerPage";

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
  const [sharing, setSharing] = useState<{ node: Node; space: FolderAccess } | null>(null);
  const [settings, setSettings] = useState<FolderAccess | null>(null);
  const once = useOnce();

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
    once(async () => {
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
    once(async () => {
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
    once(async () => {
      if (!deleting) return;
      const stale = sessionChanged();
      const target = deleting.space;
      setDeleteBusy(true);
      setDeleteError(null);
      try {
        await deleteSpace(account, target.id);
        if (stale()) return;
        setNotice(t("spaces.deleted", { name: target.name }));
        setDeleting(null);
        await refresh();
      } catch (err) {
        if (stale()) return;
        setDeleteError(failureText(err, lang));
      } finally {
        setDeleteBusy(false);
      }
    });

  const failure = q.isError ? failureText(q.error, lang) : null;
  const loaded = q.data !== undefined;

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

      {loaded && spaces.length === 0 && failure === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>{t("spaces.empty")}</p>
      )}

      {spaces.length > 0 && (
        <ul aria-label={t("manage.spaces.title")} style={{ margin: 0, padding: 0, listStyle: "none" }}>
          {spaces.map((s) => {
            const flags = [
              s.members_only ? t("spaces.flag.membersOnly") : "",
              s.sharing_managers_only ? t("spaces.flag.managersOnly") : "",
              s.no_download_for_viewers ? t("spaces.flag.noDownload") : "",
            ].filter(Boolean);
            return (
              <li
                key={s.id}
                className="row"
                style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, padding: "12px 0", borderBlockEnd: "1px solid var(--ant-color-split)" }}
              >
                <div style={{ minInlineSize: 0 }}>
                  <p style={{ margin: 0, fontWeight: 600, overflowWrap: "anywhere" }}>
                    <bdi>{s.name}</bdi>
                  </p>
                  {flags.length > 0 && (
                    <p style={{ margin: 0, fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{flags.join(" · ")}</p>
                  )}
                </div>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                  <Button size="small" onClick={() => void openShare(s)}>
                    {t("spaces.share")}
                    <span className="sr-only">: {s.name}</span>
                  </Button>
                  <Button size="small" onClick={() => setSettings(asAccess(s))}>
                    {t("spaces.settings")}
                    <span className="sr-only">: {s.name}</span>
                  </Button>
                  <Button size="small" onClick={() => setRenaming(s)}>
                    {t("spaces.rename")}
                    <span className="sr-only">: {s.name}</span>
                  </Button>
                  <Button size="small" danger onClick={() => void openDelete(s)}>
                    {t("spaces.delete")}
                    <span className="sr-only">: {s.name}</span>
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
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
          onsave={async (name) => {
            const stale = sessionChanged();
            await createSpace(account, name);
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
          onsave={async (name) => {
            const stale = sessionChanged();
            const s = renaming;
            await renameSpace(account, s.id, name);
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
