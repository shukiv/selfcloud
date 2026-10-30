// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Modal, Tag } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { rank } from "../../lib/files/roles";
import type { Permission, Role } from "../../lib/files/types";
import { listPermissions } from "../../lib/sharing/api";
import { auditLog, getPerson, listGroupMembers, listGroups, listSpaces } from "../../lib/manage/api";
import { collectAll } from "../../lib/manage/paging";
import { personLabel, statusTone } from "../../lib/manage/people";
import { failureText } from "../../lib/manage/failure";
import type { Person } from "../../lib/manage/types";
import type { AuditEntry, Names } from "../../lib/audit/sentence";
import { setPersonLimit } from "../../lib/storage/api";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import AuditItem from "./AuditItem";
import LimitEditor from "../storage/LimitEditor";
import { useOnce } from "./OwnerPage";

// PersonDialog shows one person: the details, the team spaces and groups the person belongs to, what the person did,
// and the person's own storage limit. Each is read through the API. A person's My files are never listed, since they
// are private from owners (docs/design.md, section 8).
export interface PersonDialogProps {
  account: string;
  person: Person;
  names: Names;
  onclose: () => void;
}

interface SpaceRow {
  id: string;
  name: string;
  role: Role;
}

interface Detail {
  limit: { quota_bytes: number | null; effective_quota_bytes: number | null };
  spaces: SpaceRow[];
  groups: string[];
  activity: Array<AuditEntry & { id: string; time: string }>;
}

// effectiveRole is the highest role the person holds on a team space's root: an owner manages every space, and
// otherwise a direct permission or one through a group the person is in counts. Null when it holds none.
function effectiveRole(p: Person, perms: Permission[], inGroup: Map<string, string>): Role | null {
  if (p.role === "owner") return "manager";
  let best: Role | null = null;
  for (const perm of perms) {
    const mine =
      (perm.subject_kind === "user" && perm.subject_id === p.id) || (perm.subject_kind === "group" && inGroup.has(perm.subject_id));
    if (mine && (best === null || rank(perm.role) > rank(best))) best = perm.role;
  }
  return best;
}

async function readDetail(account: string, person: Person): Promise<Detail> {
  const [gs, fs, log, self] = await Promise.all([
    collectAll((c) => listGroups(account, c)),
    collectAll((c) => listSpaces(account, c)),
    auditLog(account, { actor: person.id, limit: 10 }),
    getPerson(account, person.id),
  ]);
  const inGroup = new Map<string, string>();
  for (const g of gs.items) {
    const members = await collectAll((c) => listGroupMembers(account, g.id, c));
    if (members.items.some((m) => m.user_id === person.id)) inGroup.set(g.id, g.name);
  }
  const spaces: SpaceRow[] = [];
  for (const f of fs.items) {
    const perms = (await listPermissions(f.root_node_id)).items;
    const role = effectiveRole(person, perms, inGroup);
    if (role !== null) spaces.push({ id: f.id, name: f.name, role });
  }
  return {
    limit: { quota_bytes: self.quota_bytes ?? null, effective_quota_bytes: self.effective_quota_bytes ?? null },
    spaces,
    groups: [...inGroup.values()],
    activity: log.items,
  };
}

export default function PersonDialog({ account, person, names, onclose }: PersonDialogProps) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const once = useOnce();
  const key = queryKey("manage", account, "person", person.id);

  const q = useQuery({
    queryKey: key,
    queryFn: () => readDetail(account, person),
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const d = q.data;

  // saveLimit sets the person's limit and then reads the person again, so the dialog shows what the server applies.
  const saveLimit = (value: number | null) =>
    once(async () => {
      const stale = sessionChanged();
      setSaveError(null);
      try {
        await setPersonLimit(account, person.id, value);
        if (stale()) return;
        setEditing(false);
        setNotice(t("storage.limitSaved"));
        await qc.invalidateQueries({ queryKey: key });
      } catch (err) {
        if (stale()) return;
        setSaveError(failureText(err, lang));
      }
    });

  const limitLine = (): string => {
    const eff = d?.limit.effective_quota_bytes;
    if (eff === null || eff === undefined) return t("storage.noLimit");
    return formatBytes(eff, lang);
  };

  return (
    <Modal open title={personLabel(person)} onCancel={onclose} maskClosable={false} footer={null} width={720} destroyOnClose>
      <div style={{ display: "grid", gap: 16 }}>
        <dl style={{ display: "grid", gap: 8, margin: 0 }}>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "space-between" }}>
            <dt style={{ color: "var(--ant-color-text-secondary)" }}>{t("people.detail.email")}</dt>
            <dd style={{ margin: 0 }}>
              <bdi>{person.email}</bdi>
            </dd>
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "space-between" }}>
            <dt style={{ color: "var(--ant-color-text-secondary)" }}>{t("people.detail.role")}</dt>
            <dd style={{ margin: 0 }}>{t(`people.role.${person.role}`)}</dd>
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "space-between" }}>
            <dt style={{ color: "var(--ant-color-text-secondary)" }}>{t("people.detail.status")}</dt>
            <dd style={{ margin: 0 }}>
              <StatusTag tone={statusTone(person.status)} label={t(`people.status.${person.status}`)} />
            </dd>
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "space-between" }}>
            <dt style={{ color: "var(--ant-color-text-secondary)" }}>{t("people.detail.added")}</dt>
            <dd style={{ margin: 0 }}>{formatDate(person.created_at, lang)}</dd>
          </div>
        </dl>

        {saveError && <Alert type="error" showIcon role="alert" message={saveError} />}
        {notice && !editing && <Alert type="success" showIcon role="status" message={notice} />}

        <section id="person-limit" aria-labelledby="person-limit-title">
          <h3 id="person-limit-title" style={{ margin: "0 0 8px", fontSize: 16, fontWeight: 600 }}>
            {t("storage.limit")}
          </h3>
          {d && editing ? (
            <LimitEditor current={d.limit.quota_bytes} allowDefault onsave={saveLimit} oncancel={() => setEditing(false)} />
          ) : d ? (
            <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
              <p style={{ margin: 0 }}>
                {limitLine()}
                {d.limit.quota_bytes === null && (
                  <span style={{ color: "var(--ant-color-text-secondary)" }}> · {t("storage.followsDefault")}</span>
                )}
              </p>
              <Button
                size="small"
                onClick={() => {
                  setNotice(null);
                  setSaveError(null);
                  setEditing(true);
                }}
              >
                {t("storage.change")}
              </Button>
            </div>
          ) : null}
        </section>

        {q.isPending && <p role="status">{t("app.loading")}</p>}
        {q.isError && !d && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} />}
        {q.isError && d && <Alert type="warning" showIcon role="alert" message={failureText(q.error, lang)} />}

        {d && (
          <>
            <section aria-labelledby="person-spaces">
              <h3 id="person-spaces" style={{ margin: "0 0 8px", fontSize: 16, fontWeight: 600 }}>
                {t("people.detail.spaces")}
              </h3>
              {d.spaces.length === 0 ? (
                <p style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>{t("people.detail.noSpaces")}</p>
              ) : (
                <ul style={{ margin: 0, paddingInlineStart: 16 }}>
                  {d.spaces.map((s) => (
                    <li key={s.id}>
                      <bdi>{s.name}</bdi> — {t(`role.${s.role}`)}
                    </li>
                  ))}
                </ul>
              )}
            </section>
            <section aria-labelledby="person-groups">
              <h3 id="person-groups" style={{ margin: "0 0 8px", fontSize: 16, fontWeight: 600 }}>
                {t("people.detail.groups")}
              </h3>
              {d.groups.length === 0 ? (
                <p style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>{t("people.detail.noGroups")}</p>
              ) : (
                <ul style={{ margin: 0, paddingInlineStart: 16 }}>
                  {d.groups.map((g) => (
                    <li key={g}>
                      <bdi>{g}</bdi>
                    </li>
                  ))}
                </ul>
              )}
            </section>
            <section aria-labelledby="person-activity">
              <h3 id="person-activity" style={{ margin: "0 0 8px", fontSize: 16, fontWeight: 600 }}>
                {t("people.detail.activity")}
              </h3>
              {d.activity.length === 0 ? (
                <p style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>{t("people.detail.noActivity")}</p>
              ) : (
                <ul style={{ margin: 0, padding: 0, listStyle: "none" }}>
                  {d.activity.map((entry) => (
                    <AuditItem key={entry.id} entry={entry} names={names} />
                  ))}
                </ul>
              )}
            </section>
          </>
        )}
      </div>
    </Modal>
  );
}

// StatusTag names a state in words, with a colour that says it again. Colour alone is never the signal.
function StatusTag({ tone, label }: { tone: "ok" | "warn" | "danger" | "neutral"; label: string }) {
  const color = tone === "ok" ? "success" : tone === "warn" ? "warning" : tone === "danger" ? "error" : "default";
  return <Tag color={color}>{label}</Tag>;
}
