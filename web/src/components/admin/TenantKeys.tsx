// SPDX-License-Identifier: AGPL-3.0-or-later

import { App, Alert, Button } from "antd";
import { useFlight } from "../../lib/flight";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useRef, useState } from "react";
import { Ban } from "lucide-react";
import Listing, { type ListColumn, type ListSort } from "../files/Listing";
import { useLang, useT } from "../../lib/i18n";
import { formatDate } from "../../lib/files/format";
import { listKeys, revokeKey } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import type { ApiKey, NewKey as NewKeyReply } from "../../lib/manage/types";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import Confirm from "../Confirm";
import NewKey from "./NewKey";
import ShowOnce from "./ShowOnce";
import { useOnSessionChange } from "./hooks";
import { sectionTitle, toolbar } from "./adminStyles";
import { useAdminText } from "../../lib/manage/adminText";

// TenantKeys: the API keys of a tenant. A new key is shown once, in a dialog that holds it only while open. The key is
// never put in the query cache: the list reads name no secret, and the reply of create is kept only in this page's state.
export default function TenantKeys({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const inFlight = useRef(false);
  const keysKey = queryKey("tenant-keys", account);
  const adminText = useAdminText();
  const q = useQuery({
    queryKey: keysKey,
    queryFn: async () => (await listKeys(account)).items,
    refetchOnMount: "always",
  });
  const loaded: ApiKey[] = q.data ?? [];
  const firstFailed = q.isError && q.data === undefined;
  // The keys are loaded whole, so they are sorted here. The first state is the order the server sent.
  const [sort, setSort] = useState<{ key: "" | "name" | "created" | "last"; dir: "asc" | "desc" }>({ key: "", dir: "asc" });
  const sortBy = (key: string) => {
    if (key !== "name" && key !== "created" && key !== "last") return;
    setSort((s) => (s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: "asc" }));
  };
  const keys: ApiKey[] = useMemo(() => {
    if (sort.key === "") return loaded;
    const when = (iso: string | null) => (iso ? new Date(iso).getTime() : 0);
    const by = (k: ApiKey) => (sort.key === "name" ? k.name : sort.key === "created" ? when(k.created_at) : when(k.last_used_at));
    const sign = sort.dir === "asc" ? 1 : -1;
    return [...loaded].sort((a, b) => {
      const x = by(a);
      const y = by(b);
      const order = typeof x === "string" && typeof y === "string" ? x.localeCompare(y, lang) : (x as number) - (y as number);
      return order * sign || a.id.localeCompare(b.id);
    });
  }, [loaded, sort, lang]);
  const [creating, setCreating] = useState(false);
  const [shown, setShown] = useState<NewKeyReply | null>(null);
  const [revoking, setRevoking] = useState<ApiKey | null>(null);
  const request = useFlight();
  const { message } = App.useApp();
  const [revokeBusy, setRevokeBusy] = useState(false);
  const [revokeError, setRevokeError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  // The shown key goes when the person changes: the next person on this tab never sees it.
  // The secret and the form (with what was typed in it) are for the person who opened them: a change of person clears both.
  useOnSessionChange(() => {
    setShown(null);
    setCreating(false);
  });

  // A key that is revoked or past its expiry works no more; the list shows which.
  const keyState = (k: ApiKey): string => {
    if (k.revoked_at) return t("keys.state.revoked");
    if (k.expires_at && new Date(k.expires_at).getTime() < Date.now()) return t("keys.state.expired");
    return t("keys.state.active");
  };

  const columns: ListColumn<ApiKey>[] = [
    {
      key: "name",
      title: t("files.col.name"),
      sortKey: "name",
      render: (k) => (
        <span style={{ minWidth: 0 }}>
          <span>
            <bdi>{k.name}</bdi>
          </span>
          <span style={{ display: "block", fontFamily: "ui-monospace, monospace", fontSize: 13, overflowWrap: "anywhere", color: "var(--ant-color-text-secondary)" }}>{k.prefix}…</span>
        </span>
      ),
    },
    {
      key: "created",
      title: t("keys.created"),
      sortKey: "created",
      secondary: true,
      render: (k) => <time dateTime={k.created_at}>{formatDate(k.created_at, lang)}</time>,
    },
    {
      key: "last",
      title: t("keys.lastUsed"),
      sortKey: "last",
      secondary: true,
      render: (k) =>
        k.last_used_at ? <time dateTime={k.last_used_at}>{formatDate(k.last_used_at, lang)}</time> : <span>{t("keys.neverUsed")}</span>,
    },
    {
      key: "expires",
      title: t("keys.new.expires"),
      secondary: true,
      render: (k) => (k.expires_at ? <time dateTime={k.expires_at}>{formatDate(k.expires_at, lang)}</time> : <span>{t("keys.noExpiry")}</span>),
    },
    {
      key: "state",
      title: t("people.detail.status"),
      width: 120,
      render: (k) => <span style={{ borderRadius: 999, padding: "0 8px", background: "var(--ant-color-fill-secondary)" }}>{keyState(k)}</span>,
    },
  ];

  // cancelRevoke closes the dialog at once, busy or not: the revoke on its way is aborted, and the keys are read again, since
  // the key may have been revoked (fu4, decision 76's rule for writes).
  const cancelRevoke = () => {
    if (request.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      void qc.invalidateQueries({ queryKey: keysKey });
    }
    setRevokeError(null);
    setRevoking(null);
  };

  const confirmRevoke = async () => {
    const target = revoking;
    if (!target || inFlight.current) return;
    inFlight.current = true;
    const changed = sessionChanged();
    const signal = request.start();
    setRevokeBusy(true);
    setRevokeError(null);
    try {
      await revokeKey(account, target.id, signal);
      if (changed()) return;
      setNotice(t("keys.revokedNotice", { name: target.name }));
      setRevoking(null);
      void qc.invalidateQueries({ queryKey: keysKey });
    } catch (err) {
      if (changed() || signal.aborted) return;
      setRevokeError(failureText(err, lang));
    } finally {
      request.end();
      inFlight.current = false;
      setRevokeBusy(false);
    }
  };

  return (
    <div>
      <div style={toolbar}>
        <h2 style={sectionTitle}>{t("tenant.keys.title")}</h2>
        <div>
          <Button type="primary" onClick={() => setCreating(true)}>
            {t("keys.new.open")}
          </Button>
        </div>
      </div>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBlockEnd: 12 }} />}
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}

      {!firstFailed && q.isPending && <p role="status">{t("app.loading")}</p>}
      {/* The keys table: Name with the prefix, Created, Last used, Expires, State, and a Revoke icon. */}
      {!firstFailed && q.data !== undefined && (
        <Listing
          label={t("tenant.keys.title")}
          items={keys}
          rowLabel={(k) => k.name}
          moreLabel={(k) => t("tenants.actionsFor", { name: k.name })}
          columns={columns}
          sort={{ key: sort.key, dir: sort.dir, onSort: sortBy } satisfies ListSort}
          actions={(k) =>
            k.revoked_at
              ? []
              : [
                  {
                    key: "revoke",
                    label: `${t("keys.revoke")}: ${k.name}`,
                    icon: <Ban size={18} aria-hidden="true" focusable="false" />,
                    danger: true,
                    run: () => {
                      setRevokeError(null);
                      setRevoking(k);
                    },
                  },
                ]
          }
          empty={{
            title: t("keys.empty"),
            description: adminText ? t("admin.empty.keys") : undefined,
            action: { label: t("keys.new.open"), onClick: () => setCreating(true) },
          }}
        />
      )}

      {creating && (
        <NewKey
          account={account}
          onClose={() => setCreating(false)}
          onCreated={(k) => {
            setCreating(false);
            setShown(k);
            void qc.invalidateQueries({ queryKey: keysKey });
          }}
        />
      )}

      {shown !== null && (
        <ShowOnce
          id="new-key-once"
          title={t("keys.shown.title", { name: shown.name })}
          label={t("keys.shown.label")}
          value={shown.key}
          warning={t("keys.shown.warning")}
          onClosed={() => setShown(null)}
        />
      )}

      {revoking !== null && (
        <Confirm
          id="revoke-key"
          title={t("keys.revokeTitle", { name: revoking.name })}
          message={t("keys.revokeMessage")}
          action={t("keys.revoke")}
          danger
          error={revokeError}
          busy={revokeBusy}
          onconfirm={() => void confirmRevoke()}
          onclose={() => setRevoking(null)}
          oncancel={cancelRevoke}
        />
      )}
    </div>
  );
}
