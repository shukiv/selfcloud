// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
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
import { list, muted, row, rowMain, rowTitle, sectionTitle, toolbar } from "./adminStyles";

// TenantKeys: the API keys of a tenant. A new key is shown once, in a dialog that holds it only while open. The key is
// never put in the query cache: the list reads name no secret, and the reply of create is kept only in this page's state.
export default function TenantKeys({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const inFlight = useRef(false);
  const keysKey = queryKey("tenant-keys", account);
  const q = useQuery({
    queryKey: keysKey,
    queryFn: async () => (await listKeys(account)).items,
    refetchOnMount: "always",
  });
  const keys: ApiKey[] = q.data ?? [];
  const firstFailed = q.isError && q.data === undefined;
  const [creating, setCreating] = useState(false);
  const [shown, setShown] = useState<NewKeyReply | null>(null);
  const [revoking, setRevoking] = useState<ApiKey | null>(null);
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

  const confirmRevoke = async () => {
    const target = revoking;
    if (!target || inFlight.current) return;
    inFlight.current = true;
    const changed = sessionChanged();
    setRevokeBusy(true);
    setRevokeError(null);
    try {
      await revokeKey(account, target.id);
      if (changed()) return;
      setNotice(t("keys.revokedNotice", { name: target.name }));
      setRevoking(null);
      void qc.invalidateQueries({ queryKey: keysKey });
    } catch (err) {
      if (changed()) return;
      setRevokeError(failureText(err, lang));
    } finally {
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
      {q.isSuccess && keys.length === 0 && (
        <p role="status" style={muted}>
          {t("keys.empty")}
        </p>
      )}
      {!firstFailed && keys.length > 0 && (
        <ul style={list} aria-label={t("tenant.keys.title")}>
          {keys.map((k) => (
            <li key={k.id} style={row}>
              <div style={rowMain}>
                <p style={rowTitle}>
                  <bdi>{k.name}</bdi>
                </p>
                <p style={{ ...muted, fontFamily: "ui-monospace, monospace" }}>{k.prefix}…</p>
              </div>
              <div style={muted}>
                {t("keys.created")} <time dateTime={k.created_at}>{formatDate(k.created_at, lang)}</time>
              </div>
              <div style={muted}>
                {k.last_used_at ? t("keys.lastUsed") : t("keys.neverUsed")}
                {k.last_used_at && (
                  <>
                    {" "}
                    <time dateTime={k.last_used_at}>{formatDate(k.last_used_at, lang)}</time>
                  </>
                )}
              </div>
              <div style={muted}>
                {k.expires_at ? t("keys.expiresOn") : t("keys.noExpiry")}
                {k.expires_at && (
                  <>
                    {" "}
                    <time dateTime={k.expires_at}>{formatDate(k.expires_at, lang)}</time>
                  </>
                )}
              </div>
              <div>
                <span style={{ borderRadius: 999, padding: "0 8px", background: "var(--ant-color-fill-secondary)" }}>{keyState(k)}</span>
              </div>
              {!k.revoked_at && (
                <div>
                  <Button
                    size="small"
                    danger
                    onClick={() => {
                      setRevokeError(null);
                      setRevoking(k);
                    }}
                  >
                    {t("keys.revoke")}
                    <span className="sr-only">: {k.name}</span>
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ul>
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
        />
      )}
    </div>
  );
}
