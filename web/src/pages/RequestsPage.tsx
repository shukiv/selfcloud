// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useState } from "react";
import { Alert, Select, Skeleton } from "antd";
import { Check, X } from "lucide-react";
import Listing, { type ListAction, type ListColumn } from "../components/files/Listing";
import DriveTextFailure from "../components/drive/DriveTextFailure";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../lib/i18n";
import { useRequestsText } from "../lib/requests/text";
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
import { rank } from "../lib/files/roles";

// Requests: the access requests the caller may decide, oldest first, on one table (tl5, decision 138). Approving grants the role
// asked for, or a role the caller chooses that is not above its own; denying closes the request. An item the caller cannot see is
// listed without its name. A person with no name in the answer is shown as "A person", never by an ID. Who and Asks sort on this
// page, since the whole list is loaded; a press on a header sorts it, and a second press turns it over.
interface Row {
  id: string;
  req: AccessRequest;
  node: Node | null;
}

type SortKey = "who" | "asks";

// capFor says the highest role the caller may grant on the item, from its role there. No item, no grant.
function capFor(node: Node | null): Role[] {
  if (node === null) return [];
  return grantableRoles(shareCap({ role: node.role, editorsCanShare: node.editors_can_share, managersOnly: false }));
}

// readRows reads the requests the caller may decide, each with its item when the caller can see it.
async function readRows(): Promise<Row[]> {
  const { items } = await listAccessRequests();
  return Promise.all(items.map(async (req) => ({ id: req.id, req, node: await statOrNull(req.node_id) })));
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
  const hasData = q.data !== undefined;
  const rt = useRequestsText();
  const [sortKey, setSortKey] = useState<SortKey | null>(null);
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");
  const who = (req: AccessRequest): string => req.user_name || t("share.subject.person");
  const rows = useMemo(() => {
    const base = q.data ?? [];
    if (sortKey === null) return base;
    const dir = sortDir === "asc" ? 1 : -1;
    const cmp = (a: Row, b: Row) =>
      sortKey === "asks" ? rank(a.req.role) - rank(b.req.role) : who(a.req).localeCompare(who(b.req), lang);
    return [...base].sort((a, b) => dir * cmp(a, b));
    // who reads the translator, which is in the page's language; the rows are sorted again when the language changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q.data, sortKey, sortDir, lang, t]);

  // pressSort sorts by a header's column, and turns the sort over when the same header is pressed again.
  const pressSort = (key: SortKey) => {
    if (key === sortKey) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else {
      setSortKey(key);
      setSortDir("asc");
    }
  };

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

  // The columns: the name cell is Who; Item and Message are a second line on a phone; Grant is the role's Select (its own
  // control, which stays on a phone); Approve and Deny are the row's icons, and the ⋮ menu on a phone.
  const columns: ListColumn<Row>[] = [
    {
      key: "who",
      title: t("requests.colWho"),
      sortKey: "who",
      render: (row) => <span style={{ fontWeight: 600, overflowWrap: "anywhere" }}><bdi>{who(row.req)}</bdi></span>,
    },
    {
      key: "item",
      title: t("requests.colItem"),
      secondary: true,
      render: (row) =>
        row.node ? (
          <a href={`/n/${row.node.id}`}>
            <bdi>{row.node.name}</bdi>
          </a>
        ) : (
          <span style={{ color: "var(--ant-color-text-secondary)" }}>{t("requests.hiddenItem")}</span>
        ),
    },
    {
      key: "asks",
      title: t("requests.colAsks"),
      sortKey: "asks",
      render: (row) => <span style={{ color: "var(--ant-color-text-secondary)" }}>{t("requests.asks", { role: t(`role.${row.req.role}`) })}</span>,
    },
    {
      key: "message",
      title: t("requests.colMessage"),
      secondary: true,
      render: (row) => (row.req.message ? <bdi style={{ overflowWrap: "anywhere" }}>{row.req.message}</bdi> : null),
    },
    {
      key: "grant",
      title: t("requests.grant"),
      width: 170,
      render: (row) => {
        const roles = capFor(row.node);
        if (roles.length === 0) return null;
        return (
          <Select
            id={`grant-${row.req.id}`}
            aria-label={t("requests.grant")}
            value={grants[row.req.id] ?? row.req.role}
            onChange={(r: Role) => setGrants((g) => ({ ...g, [row.req.id]: r }))}
            options={roles.map((r) => ({ value: r, label: t(`role.${r}`) }))}
            style={{ width: "100%", minWidth: 120 }}
          />
        );
      },
    },
  ];

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("requests.title")}</h1>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBottom: 12 }} />}
      {actionError !== null && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBottom: 12 }} />}
      {failure !== null && (
        <Alert type={hasData ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />
      )}

      {!hasData && q.isPending && signedIn && <Skeleton active />}

      {rt.failed ? (
        <DriveTextFailure retry={rt.retry} />
      ) : !rt.ready ? null : (
        hasData && (
          <div className="requests">
            <Listing<Row>
              label={t("requests.title")}
              items={rows}
              rowLabel={(row) => who(row.req)}
              rowClassName={() => "request"}
              sort={{ key: sortKey ?? "", dir: sortDir, onSort: (k) => pressSort(k as SortKey) }}
              columns={columns}
              actions={(row): ListAction[] => {
                const name = who(row.req);
                return [
                  {
                    key: "approve",
                    label: `${t("requests.approve")}: ${name}`,
                    icon: <Check size={18} aria-hidden="true" focusable="false" />,
                    disabled: decide.isPending,
                    run: () => decide.mutate({ row, approve: true }),
                  },
                  {
                    key: "deny",
                    label: `${t("requests.deny")}: ${name}`,
                    icon: <X size={18} aria-hidden="true" focusable="false" />,
                    disabled: decide.isPending,
                    run: () => decide.mutate({ row, approve: false }),
                  },
                ];
              }}
              empty={{ title: t("requests.emptyTitle"), description: t("requests.empty") }}
            />
          </div>
        )
      )}
    </div>
  );
}
