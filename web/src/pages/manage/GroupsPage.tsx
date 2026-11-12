// SPDX-License-Identifier: AGPL-3.0-or-later
import { Pencil, Trash2, UserMinus, Users } from "lucide-react";

import { useMemo, useState } from "react";
import { App, Alert, Button } from "antd";
import { useFlight } from "../../lib/flight";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { addGroupMember, createGroup, deleteGroup, listGroupMembers, listGroups, removeGroupMember, renameGroup } from "../../lib/manage/api";
import { collectAll } from "../../lib/manage/paging";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { useAdminText } from "../../lib/manage/adminText";
import type { Group } from "../../lib/manage/types";
import type { Names } from "../../lib/audit/sentence";
import type { DirectoryEntry } from "../../lib/files/types";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import Listing, { type ListColumn } from "../../components/files/Listing";
import PeopleCombobox from "../../components/share/PeopleCombobox";
import Confirm from "../../components/Confirm";
import NameDialog from "../../components/manage/NameDialog";
import { OwnerOnly, useAccountID, useManageTitle, useOncePer } from "../../components/manage/OwnerPage";
import { LOCK } from "../../lib/manage/locks";

// EMPTY is module-level, so "no data" stays the same array between renders.
const EMPTY: string[] = [];

// useMemberRows builds a group's rows. The row memo in Listing compares the row object, so the rows must keep their
// identity: a new array with new rows would redraw each row's end on every render. The page's actions need the current row.
export function useMemberRows(ids: string[] | undefined, lookup: Names, unknown: string): { id: string; name: string }[] {
  return useMemo(() => (ids ?? EMPTY).map((id) => ({ id, name: lookup.person(id) ?? unknown })), [ids, lookup, unknown]);
}

// Groups: a group is a named set of people. A permission on a team space given to a group reaches each member at once,
// and removing a member ends that access at once. The groups and a group's members are the one Listing (design-tl 2).
function GroupsView() {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  useManageTitle("manage.groups.title");
  const [notice, setNotice] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState<Group | null>(null);
  const [deleting, setDeleting] = useState<Group | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const flight = useFlight();
  const { message } = App.useApp();
  const [open, setOpen] = useState<Group | null>(null);
  const [adding, setAdding] = useState<DirectoryEntry | null>(null);
  const [comboKey, setComboKey] = useState(0);
  const [memberError, setMemberError] = useState<string | null>(null);
  const once = useOncePer();
  const adminText = useAdminText();

  const q = useInfiniteQuery({
    queryKey: queryKey("manage", account, "groups"),
    queryFn: ({ pageParam }) => listGroups(account, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const groups: Group[] = q.data?.pages.flatMap((p) => p.items) ?? [];

  const members = useQuery({
    queryKey: queryKey("manage", account, "group-members", open?.id ?? ""),
    queryFn: async () => {
      if (!open) return [];
      return (await collectAll((c) => listGroupMembers(account, open.id, c))).items.map((m) => m.user_id);
    },
    enabled: open !== null && account !== "",
    refetchOnMount: "always",
  });
  const names = useQuery({
    queryKey: queryKey("manage", account, "names"),
    queryFn: () => loadNames(account),
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const lookup = names.data?.names ?? NO_NAMES;

  // The heading takes the name the list has now: after a rename the snapshot taken at open would show the old name.
  const listed = q.data?.pages.flatMap((p) => p.items).find((g) => g.id === open?.id);
  const shownName = listed?.name ?? open?.name ?? "";
  const refreshGroups = () => qc.invalidateQueries({ queryKey: queryKey("manage", account, "groups") });
  const refreshMembers = () => qc.invalidateQueries({ queryKey: queryKey("manage", account, "group-members") });

  const addMember = () =>
    once(LOCK.groupAdd(open?.id ?? ""), async () => {
      if (!open || !adding) return;
      const stale = sessionChanged();
      setMemberError(null);
      try {
        await addGroupMember(account, open.id, adding.id);
        if (stale()) return;
        setAdding(null);
        setComboKey((k) => k + 1);
        await refreshMembers();
      } catch (err) {
        if (stale()) return;
        setMemberError(failureText(err, lang));
      }
    });

  const removeMember = (usr: string) =>
    once(LOCK.groupMember(open?.id ?? "", usr), async () => {
      if (!open) return;
      const stale = sessionChanged();
      setMemberError(null);
      try {
        await removeGroupMember(account, open.id, usr);
        if (stale()) return;
        await refreshMembers();
      } catch (err) {
        if (stale()) return;
        setMemberError(failureText(err, lang));
      }
    });

  // cancelDelete closes the dialog at once, busy or not: the delete on its way is aborted, and the groups are read again,
  // since the group may have been deleted (fu4, decision 76's rule for writes).
  const cancelDelete = () => {
    if (flight.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      void refreshGroups();
    }
    setDeleteError(null);
    setDeleting(null);
  };

  const confirmDelete = () =>
    once(LOCK.groupDelete(deleting?.id ?? ""), async () => {
      if (!deleting) return;
      const stale = sessionChanged();
      const target = deleting;
      const signal = flight.start();
      setDeleteBusy(true);
      setDeleteError(null);
      try {
        await deleteGroup(account, target.id, signal);
        if (stale()) return;
        if (open?.id === target.id) setOpen(null);
        setNotice(t("groups.deleted", { name: target.name }));
        setDeleting(null);
        await refreshGroups();
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

  // The groups: one name column, and the three icons of a row (each with its tooltip, and a name for a screen reader).
  const groupColumns: ListColumn<Group>[] = [{ key: "name", title: t("files.col.name"), render: (g) => <bdi>{g.name}</bdi> }];
  // A group's members: one name column and a Remove icon, which takes the member's name in its label.
  const memberRows = useMemberRows(members.data, lookup, t("people.unknown"));
  const memberColumns: ListColumn<{ id: string; name: string }>[] = [{ key: "name", title: t("files.col.name"), render: (m) => <bdi>{m.name}</bdi> }];

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {t("manage.groups.title")}
        </h1>
        <Button type="primary" onClick={() => setCreating(true)}>
          {t("groups.new")}
        </Button>
      </div>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {!loaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {loaded && (
        <Listing
          label={t("manage.groups.title")}
          items={groups}
          rowLabel={(g) => g.name}
          columns={groupColumns}
          actions={(g) => [
            {
              key: "members",
              label: `${t("groups.members")}: ${g.name}`,
              icon: <Users size={18} aria-hidden="true" focusable="false" />,
              // The open group's Members icon is pressed: the members under the list are its (design-tl 2.2, decision 114).
              // Only that icon has a pressed state; a second click on it closes the members (review M1).
              pressed: open?.id === g.id ? true : undefined,
              run: () => {
                setMemberError(null);
                setAdding(null);
                setOpen(open?.id === g.id ? null : g);
              },
            },
            {
              key: "rename",
              label: `${t("groups.rename")}: ${g.name}`,
              icon: <Pencil size={18} aria-hidden="true" focusable="false" />,
              run: () => setRenaming(g),
            },
            {
              key: "delete",
              label: `${t("groups.delete")}: ${g.name}`,
              icon: <Trash2 size={18} aria-hidden="true" focusable="false" />,
              danger: true,
              run: () => {
                setDeleteError(null);
                setDeleting(g);
              },
            },
          ]}
          empty={{
            title: t("groups.empty"),
            description: adminText ? t("admin.empty.groups") : undefined,
            action: { label: t("groups.new"), onClick: () => setCreating(true) },
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

      {open && (
        <section aria-labelledby="members-title" style={{ marginBlockStart: 24 }}>
          <h2 id="members-title" style={{ fontSize: 18, fontWeight: 600, margin: "0 0 8px" }}>
            <bdi>{shownName}</bdi>
          </h2>
          {memberError !== null && <Alert type="error" showIcon role="alert" message={memberError} style={{ marginBottom: 12 }} />}
          {members.isError && <Alert type="error" showIcon role="alert" message={failureText(members.error, lang)} style={{ marginBottom: 12 }} />}
          {members.data !== undefined && (
            <Listing
              label={t("groups.members")}
              items={memberRows}
              rowLabel={(m) => m.name}
              columns={memberColumns}
              actions={(m) => [
                {
                  key: "remove",
                  label: `${t("groups.removeMember")}: ${m.name}`,
                  icon: <UserMinus size={18} aria-hidden="true" focusable="false" />,
                  danger: true,
                  run: () => void removeMember(m.id),
                },
              ]}
              empty={{ title: t("groups.noMembers") }}
            />
          )}
          <div style={{ marginBlockStart: 12 }} key={comboKey}>
            <PeopleCombobox
              id="group-add"
              kind="user"
              label={t("groups.addPerson")}
              hint={t("groups.addHint")}
              onpick={(entry) => setAdding(entry)}
            />
          </div>
          <div style={{ marginBlockStart: 8 }}>
            <Button type="primary" disabled={!adding} onClick={() => void addMember()}>
              {t("groups.add")}
            </Button>
          </div>
        </section>
      )}

      {creating && (
        <NameDialog
          id="group-new"
          title={t("groups.new")}
          label={t("groups.nameLabel")}
          action={t("groups.create")}
          onclose={() => setCreating(false)}
          onsave={async (name, signal) => {
            const stale = sessionChanged();
            await createGroup(account, name, signal);
            setCreating(false);
            if (stale()) return;
            await refreshGroups();
          }}
        />
      )}

      {renaming && (
        <NameDialog
          id="group-rename"
          title={t("groups.rename")}
          label={t("groups.nameLabel")}
          initial={renaming.name}
          action={t("groups.save")}
          onclose={() => setRenaming(null)}
          onsave={async (name, signal) => {
            const stale = sessionChanged();
            const g = renaming;
            await renameGroup(account, g.id, name, signal);
            setRenaming(null);
            if (stale()) return;
            await refreshGroups();
          }}
        />
      )}

      {deleting && (
        <Confirm
          id="group-delete"
          title={t("groups.deleteTitle", { name: deleting.name })}
          message={t("groups.deleteMessage")}
          action={t("groups.delete")}
          danger
          busy={deleteBusy}
          error={deleteError}
          onconfirm={() => void confirmDelete()}
          onclose={() => setDeleting(null)}
          oncancel={cancelDelete}
        />
      )}
    </div>
  );
}

export default function GroupsPage() {
  return (
    <OwnerOnly section="groups">
      <GroupsView />
    </OwnerOnly>
  );
}
