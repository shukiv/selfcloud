// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { Alert, Button, Select, Skeleton, Space } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { ApiError } from "../lib/apiError";
import { sessionChanged, session } from "../lib/session";
import { queryKey } from "../lib/query";
import { fileErrorText } from "../lib/files/fileErrors";
import { statOrNull } from "../lib/files/api";
import type { AccessRequest, Node, Role } from "../lib/files/types";
import { approveAccessRequest, denyAccessRequest, listAccessRequests } from "../lib/sharing/api";
import { grantableRoles, shareCap } from "../lib/sharing/roles";
import { refreshPendingRequests } from "../lib/sharing/requestCount";

// Requests: the access requests the caller may decide, oldest first. Approving grants the role asked for, or a role
// the caller chooses that is not above its own; denying closes the request. An item the caller cannot see is listed
// without its name. A person with no name in the answer is shown as "A person", never by an ID.
interface Row {
  req: AccessRequest;
  node: Node | null;
}

// capFor says the highest role the caller may grant on the item, from its role there. No item, no grant.
function capFor(node: Node | null): Role[] {
  if (node === null) return [];
  return grantableRoles(shareCap({ role: node.role, editorsCanShare: node.editors_can_share, managersOnly: false }));
}

// readRows reads the requests the caller may decide, each with its item when the caller can see it.
async function readRows(): Promise<Row[]> {
  const { items } = await listAccessRequests();
  return Promise.all(items.map(async (req) => ({ req, node: await statOrNull(req.node_id) })));
}

export default function RequestsPage() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const signedIn = useStore(session) !== null;
  const qc = useQueryClient();
  const [notice, setNotice] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  // grants holds the role the approver chose for each request; it starts at the role the request asks for.
  const [grants, setGrants] = useState<Record<string, Role>>({});

  const q = useQuery({
    queryKey: queryKey("requests"),
    queryFn: readRows,
    enabled: signedIn,
    refetchOnMount: "always",
  });
  const rows = q.data ?? [];
  const hasData = q.data !== undefined;

  useEffect(() => {
    document.title = `${t("requests.title")} · ${brandName}`;
  }, [t, brandName]);

  // The count in the menu is read on every open of this page, as Svelte does, not only when the session changes.
  useEffect(() => {
    void refreshPendingRequests();
  }, []);

  // decisionError says what a refused decision means, by its code. The server's text is never shown.
  const decisionError = (err: unknown): string => {
    if (err instanceof ApiError) {
      if (err.code === "conflict") return t("requests.err.conflict");
      if (err.code === "forbidden") return t("requests.err.forbidden");
    }
    return fileErrorText(err, lang);
  };

  const who = (req: AccessRequest): string => req.user_name || t("share.subject.person");

  // decide sends one decision, then refreshes the list and the count, unless the session has ended meanwhile.
  const decide = useMutation({
    mutationFn: async ({ row, approve }: { row: Row; approve: boolean }) => {
      const grant = grants[row.req.id] ?? row.req.role;
      if (approve) await approveAccessRequest(row.req.id, grant === row.req.role ? undefined : grant);
      else await denyAccessRequest(row.req.id);
      return { row, approve };
    },
    // The session is watched from the moment the decision is sent. A mutation's callbacks still run after the query
    // client is cleared at a change of person, so each callback checks the watch before it shows anything.
    onMutate: () => {
      setActionError(null);
      setNotice(null);
      return { changed: sessionChanged() };
    },
    onSuccess: async ({ row, approve }, _vars, ctx) => {
      if (ctx.changed()) return;
      const name = who(row.req);
      setNotice(approve ? t("requests.approved", { name }) : t("requests.denied", { name }));
      await qc.invalidateQueries({ queryKey: queryKey("requests") });
      void refreshPendingRequests();
    },
    onError: (err, _vars, ctx) => {
      if (ctx?.changed()) return;
      setActionError(decisionError(err));
    },
  });

  const failure = q.isError ? fileErrorText(q.error, lang) : null;

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("requests.title")}</h1>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {actionError !== null && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBottom: 12 }} />}
      {failure !== null && (
        <Alert type={hasData ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />
      )}

      {!hasData && q.isPending && signedIn && <Skeleton active />}

      {hasData && rows.length === 0 && failure === null && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>{t("requests.empty")}</p>
      )}

      {rows.length > 0 && (
        <ul className="requests" aria-label={t("requests.title")} style={{ display: "grid", gap: 12, margin: 0, padding: 0, listStyle: "none" }}>
          {rows.map((row) => {
            const roles = capFor(row.node);
            const grant = grants[row.req.id] ?? row.req.role;
            const name = who(row.req);
            return (
              <li
                key={row.req.id}
                className="request"
                aria-label={name}
                style={{ display: "grid", gap: 8, padding: 16, border: "1px solid var(--ant-color-border)", borderRadius: 4 }}
              >
                <div style={{ display: "flex", flexWrap: "wrap", alignItems: "baseline", gap: "8px 12px" }}>
                  <span style={{ fontWeight: 600, overflowWrap: "anywhere" }}>
                    <bdi>{name}</bdi>
                  </span>
                  <span style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
                    {t("requests.asks", { role: t(`role.${row.req.role}`) })}
                  </span>
                </div>
                <p style={{ margin: 0, overflowWrap: "anywhere" }}>
                  {row.node ? (
                    <a href={`/n/${row.node.id}`}>
                      <bdi>{row.node.name}</bdi>
                    </a>
                  ) : (
                    <span style={{ color: "var(--ant-color-text-secondary)" }}>{t("requests.hiddenItem")}</span>
                  )}
                </p>
                {row.req.message && (
                  <blockquote style={{ margin: 0, padding: 0, color: "var(--ant-color-text-secondary)" }}>
                    <bdi>{row.req.message}</bdi>
                  </blockquote>
                )}
                <Space wrap align="center">
                  {roles.length > 0 && (
                    <>
                      <label htmlFor={`grant-${row.req.id}`}>{t("requests.grant")}</label>
                      <Select
                        id={`grant-${row.req.id}`}
                        aria-label={t("requests.grant")}
                        value={grant}
                        onChange={(r: Role) => setGrants((g) => ({ ...g, [row.req.id]: r }))}
                        options={roles.map((r) => ({ value: r, label: t(`role.${r}`) }))}
                        style={{ minWidth: 160 }}
                      />
                    </>
                  )}
                  <Button type="primary" disabled={decide.isPending} onClick={() => decide.mutate({ row, approve: true })}>
                    {t("requests.approve")}
                  </Button>
                  <Button disabled={decide.isPending} onClick={() => decide.mutate({ row, approve: false })}>
                    {t("requests.deny")}
                  </Button>
                </Space>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
