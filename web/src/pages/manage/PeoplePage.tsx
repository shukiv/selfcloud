// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Dropdown, Tag, type MenuProps } from "antd";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { listPeople, resendVerification, sendPasswordReset, updatePerson } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { personLabel, statusTone } from "../../lib/manage/people";
import type { Person, PersonReply } from "../../lib/manage/types";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import InviteDialog from "../../components/manage/InviteDialog";
import PersonDialog from "../../components/manage/PersonDialog";
import RemoveDialog from "../../components/manage/RemoveDialog";
import { OwnerOnly, useAccountID, useManageTitle, useOnce } from "../../components/manage/OwnerPage";

// People: the account's owners and members, invited by email. An owner changes a person's role or status and removes a
// person here. The server answers every change; a refusal is shown by its code and reason.
function PeopleView() {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  useManageTitle("manage.people.title");
  const [notice, setNotice] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyRow, setBusyRow] = useState<string | null>(null);
  const [inviting, setInviting] = useState(false);
  const [detail, setDetail] = useState<Person | null>(null);
  const [removing, setRemoving] = useState<Person | null>(null);
  const once = useOnce();

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
    once(async () => {
      const stale = sessionChanged();
      setBusyRow(p.id);
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
        setBusyRow(null);
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

  const menuFor = (p: Person): MenuProps["items"] => {
    const items: NonNullable<MenuProps["items"]> = [];
    if (p.status === "pending") items.push({ key: "resend", label: t("people.resend"), onClick: () => void resend(p) });
    if (p.status === "active") items.push({ key: "reset", label: t("people.reset"), onClick: () => void reset(p) });
    items.push({
      key: "role",
      label: p.role === "owner" ? t("people.makeMember") : t("people.makeOwner"),
      onClick: () => void changeRole(p),
    });
    if (p.status !== "pending") {
      items.push({
        key: "status",
        label: p.status === "disabled" ? t("people.enable") : t("people.disable"),
        onClick: () => void changeStatus(p),
      });
    }
    items.push({ key: "remove", label: t("people.remove.open"), danger: true, onClick: () => setRemoving(p) });
    return items;
  };

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

      {loaded && people.length === 0 && failure === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>{t("people.empty")}</p>
      )}

      {people.length > 0 && (
        <ul aria-label={t("manage.people.title")} style={{ margin: 0, padding: 0, listStyle: "none" }}>
          {people.map((p) => (
            <li
              key={p.id}
              className="row person-row"
              style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 12, padding: "12px 0", borderBlockEnd: "1px solid var(--ant-color-split)" }}
            >
              <div style={{ flex: "1 1 200px", minInlineSize: 0 }}>
                <p style={{ margin: 0, fontWeight: 600, overflowWrap: "anywhere" }}>
                  <bdi>{personLabel(p)}</bdi>
                </p>
                <p style={{ margin: 0, color: "var(--ant-color-text-secondary)", overflowWrap: "anywhere" }}>
                  <bdi>{p.email}</bdi>
                </p>
              </div>
              <div>
                <Tag>{t(`people.role.${p.role}`)}</Tag>
              </div>
              <div>
                <StatusTag tone={statusTone(p.status)} label={t(`people.status.${p.status}`)} />
              </div>
              <div style={{ display: "flex", gap: 8 }}>
                <Button size="small" onClick={() => setDetail(p)}>
                  {t("people.open")}
                  <span className="sr-only">: {personLabel(p)}</span>
                </Button>
                <Dropdown menu={{ items: menuFor(p) }} trigger={["click"]} disabled={busyRow === p.id}>
                  <Button size="small" aria-haspopup="menu" aria-label={t("people.actionsFor", { name: personLabel(p) })} disabled={busyRow === p.id}>
                    …
                  </Button>
                </Dropdown>
              </div>
            </li>
          ))}
        </ul>
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
