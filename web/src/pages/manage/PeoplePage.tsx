// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Tag, theme as antdTheme } from "antd";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import Listing, { type ListColumn, type ListMenuEntry } from "../../components/files/Listing";
import { useLang, useT } from "../../lib/i18n";
import { listPeople, resendVerification, sendPasswordReset, updatePerson } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { personLabel, statusTone } from "../../lib/manage/people";
import { useAdminText } from "../../lib/manage/adminText";
import type { Person, PersonReply } from "../../lib/manage/types";
import { queryKey } from "../../lib/query";
import { session, sessionChanged } from "../../lib/session";
import { useStore } from "../../lib/store";
import InviteDialog from "../../components/manage/InviteDialog";
import PersonDialog from "../../components/manage/PersonDialog";
import RemoveDialog from "../../components/manage/RemoveDialog";
import { OwnerOnly, useAccountID, useManageTitle, useOncePer } from "../../components/manage/OwnerPage";
import { LOCK } from "../../lib/manage/locks";

// People: the account's owners and members, invited by email. An owner changes a person's role or status and removes a
// person here. The server answers every change; a refusal is shown by its code and reason. The list is the one Listing:
// A row opens the person, and the row's ⋮ menu holds the changes.
function PeopleView() {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  useManageTitle("manage.people.title");
  const [notice, setNotice] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  // The rows with an action out: each row's own, so one row's end does not clear another row's busy state.
  const [busyRows, setBusyRows] = useState<ReadonlySet<string>>(() => new Set());
  const [inviting, setInviting] = useState(false);
  const [detail, setDetail] = useState<Person | null>(null);
  const [removing, setRemoving] = useState<Person | null>(null);
  const once = useOncePer();
  const adminText = useAdminText();
  const { token } = antdTheme.useToken();
  // The signed-in person's own row reads You.
  const me = useStore(session)?.user?.id;

  const q = useInfiniteQuery({
    queryKey: queryKey("manage", account, "people"),
    queryFn: ({ pageParam }) => listPeople(account, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  // The names only label the audit sentences; when they cannot load, the list still shows with neutral words.
  const names = useQuery({
    queryKey: queryKey("manage", account, "names"),
    queryFn: () => loadNames(account),
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const people: Person[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const lookup = names.data?.names ?? NO_NAMES;

  const refresh = () => qc.invalidateQueries({ queryKey: queryKey("manage", account, "people") });

  // sentNotice says whether a mail went out, by the answer's own flag.
  const sentNotice = (reply: PersonReply): string =>
    reply.verification?.sent === false ? t("people.mailNotSent") : t("people.mailSent");

  // act runs one change on a row: one at a time, and an answer for a person who has gone is not shown.
  const act = (p: Person, fn: () => Promise<string | null>) =>
    once(LOCK.person(p.id), async () => {
      const stale = sessionChanged();
      setBusyRows((s) => new Set(s).add(p.id));
      setActionError(null);
      setNotice(null);
      try {
        const msg = await fn();
        if (stale()) return;
        setNotice(msg);
        await refresh();
      } catch (err) {
        if (stale()) return;
        setActionError(failureText(err, lang));
      } finally {
        setBusyRows((s) => {
          const next = new Set(s);
          next.delete(p.id);
          return next;
        });
      }
    });

  const resend = (p: Person) => act(p, async () => sentNotice(await resendVerification(account, p.id, "email")));
  const reset = (p: Person) => act(p, async () => sentNotice(await sendPasswordReset(account, p.id)));
  const changeRole = (p: Person) =>
    act(p, async () => {
      await updatePerson(account, p.id, { role: p.role === "owner" ? "member" : "owner" });
      return null;
    });
  const changeStatus = (p: Person) =>
    act(p, async () => {
      await updatePerson(account, p.id, { status: p.status === "disabled" ? "active" : "disabled" });
      return null;
    });

  // menuFor is the row's ⋮ menu: only what the person's state allows. A row with a change on its way has its entries off.
  const menuFor = (p: Person): ListMenuEntry[] => {
    const busy = busyRows.has(p.id);
    const items: ListMenuEntry[] = [];
    if (p.status === "pending") items.push({ key: "resend", label: t("people.resend"), disabled: busy, onSelect: () => void resend(p) });
    if (p.status === "active") items.push({ key: "reset", label: t("people.reset"), disabled: busy, onSelect: () => void reset(p) });
    items.push({
      key: "role",
      label: p.role === "owner" ? t("people.makeMember") : t("people.makeOwner"),
      disabled: busy,
      onSelect: () => void changeRole(p),
    });
    if (p.status !== "pending") {
      items.push({
        key: "status",
        label: p.status === "disabled" ? t("people.enable") : t("people.disable"),
        disabled: busy,
        onSelect: () => void changeStatus(p),
      });
    }
    items.push({ key: "remove", label: t("people.remove.open"), danger: true, disabled: busy, onSelect: () => setRemoving(p) });
    return items;
  };

  const columns: ListColumn<Person>[] = [
    {
      key: "name",
      title: t("files.col.name"),
      render: (p) => (
        <span style={{ minWidth: 0 }}>
          <span>
            {/* The name is a link-like button: a keyboard user opens the person from it. It stops the click here, so the row does not open it twice. */}
            <Button
              type="link"
              onClick={(e) => {
                e.stopPropagation();
                setDetail(p);
              }}
              style={{ padding: 0, height: "auto", whiteSpace: "normal", textAlign: "start" }}
            >
              <bdi>{personLabel(p)}</bdi>
            </Button>
            {p.id === me && <Tag style={{ marginInlineStart: 8 }}>{t("share.you")}</Tag>}
          </span>
          <span style={{ display: "block", fontSize: 13, overflowWrap: "anywhere", color: token.colorTextSecondary }}>
            <bdi>{p.email}</bdi>
          </span>
        </span>
      ),
    },
    { key: "role", title: t("people.detail.role"), secondary: true, width: 140, render: (p) => <Tag>{t(`people.role.${p.role}`)}</Tag> },
    {
      key: "status",
      title: t("people.detail.status"),
      secondary: true,
      width: 160,
      render: (p) => <StatusTag tone={statusTone(p.status)} label={t(`people.status.${p.status}`)} />,
    },
  ];

  const failure = q.isError ? failureText(q.error, lang) : null;
  const loaded = q.data !== undefined;

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {t("manage.people.title")}
        </h1>
        <Button type="primary" onClick={() => setInviting(true)}>
          {t("people.invite.open")}
        </Button>
      </div>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {actionError !== null && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBottom: 12 }} />}
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {!loaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {/* The table stays on screen when a refetch fails; with no first load, there is no table to keep. */}
      {loaded && (
        <Listing
          label={t("manage.people.title")}
          items={people}
          rowLabel={personLabel}
          moreLabel={(p) => t("people.actionsFor", { name: personLabel(p) })}
          columns={columns}
          menu={menuFor}
          onOpen={(p) => setDetail(p)}
          empty={{
            title: t("people.empty"),
            description: adminText ? t("admin.empty.people") : undefined,
            action: { label: t("people.invite.open"), onClick: () => setInviting(true) },
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

      {inviting && (
        <InviteDialog
          account={account}
          onclose={() => setInviting(false)}
          oncreated={(reply) => {
            setInviting(false);
            setNotice(sentNotice(reply));
            void refresh();
          }}
        />
      )}

      {detail && <PersonDialog account={account} person={detail} names={lookup} onclose={() => setDetail(null)} />}

      {removing && (
        <RemoveDialog
          account={account}
          person={removing}
          candidates={people}
          onclose={() => setRemoving(null)}
          ondone={() => {
            const name = personLabel(removing);
            setRemoving(null);
            setNotice(t("people.removed", { name }));
            void refresh();
          }}
        />
      )}
    </div>
  );
}

// StatusTag names a state in words, with a colour that says it again.
function StatusTag({ tone, label }: { tone: "ok" | "warn" | "danger" | "neutral"; label: string }) {
  const color = tone === "ok" ? "success" : tone === "warn" ? "warning" : tone === "danger" ? "error" : "default";
  return <Tag color={color}>{label}</Tag>;
}

export default function PeoplePage() {
  return (
    <OwnerOnly section="people">
      <PeopleView />
    </OwnerOnly>
  );
}
