// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { Alert, Button, Input, Select } from "antd";
import { useLang, useT, translate, translatePlural, type Lang } from "../../lib/i18n";
import { errorMessage } from "../../lib/errors";
import { createAccessRequest } from "../../lib/sharing/api";
import { REQUEST_SENT_KEY, requestOutcome } from "../../lib/sharing/requests";
import type { Role } from "../../lib/files/types";

// RequestAccess asks the people who manage an item for a role on it. The server answers 202 whether or not the item
// exists, so the confirmation is the same whatever happens; only a rate limit or a lost connection is said apart, since
// those say nothing about the item. The parent keys it on the item, so an answer never carries over to another one.
type Asked = Exclude<Role, "contributor" | "manager">;
const ROLES: Asked[] = ["viewer", "commenter", "editor"];

export default function RequestAccess({ nodeID }: { nodeID: string }) {
  const { t } = useT();
  const lang = useLang();
  const [role, setRole] = useState<Asked>("viewer");
  const [message, setMessage] = useState("");
  const [sending, setSending] = useState(false);
  const [outcome, setOutcome] = useState<"idle" | "sent" | "limited" | "failed">("idle");
  const [limitText, setLimitText] = useState("");

  const send = async (e: FormEvent) => {
    e.preventDefault();
    if (message.length > 500 || sending) return;
    setSending(true);
    setLimitText("");
    const text = message.trim();
    try {
      await createAccessRequest(nodeID, role, text);
      setOutcome("sent");
    } catch (err) {
      const kind = requestOutcome(err);
      setOutcome(kind);
      if (kind === "limited") {
        const msg = errorMessage(err);
        setLimitText(msg.plural !== undefined ? translatePlural(lang, msg.key, msg.plural) : translate(lang as Lang, msg.key));
      }
    } finally {
      setSending(false);
    }
  };

  return (
    <section className="request" aria-labelledby="request-title" style={{ display: "grid", gap: 12, marginBlockStart: 24, maxWidth: 420 }}>
      <h2 id="request-title" style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
        {t("share.request.title")}
      </h2>
      {outcome === "sent" ? (
        <p className="sent" role="status" style={{ margin: 0 }}>
          {t(REQUEST_SENT_KEY)}
        </p>
      ) : (
        <form onSubmit={send} style={{ display: "grid", gap: 12 }}>
          <div style={{ display: "grid", gap: 8 }}>
            <label htmlFor="request-role">{t("share.request.role")}</label>
            <Select
              id="request-role"
              value={role}
              virtual={false}
              onChange={(v: Asked) => setRole(v)}
              options={ROLES.map((r) => ({ value: r, label: t(`role.${r}`) }))}
              disabled={sending}
            />
          </div>
          <div style={{ display: "grid", gap: 8 }}>
            <label htmlFor="request-message">{t("share.request.message")}</label>
            <Input.TextArea
              id="request-message"
              rows={3}
              maxLength={500}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              aria-describedby="request-message-hint"
            />
            <p className="hint" id="request-message-hint" style={{ margin: 0, fontSize: 13 }}>
              {t("share.messageHint", { count: message.length })}
            </p>
          </div>
          {(outcome === "limited" || outcome === "failed") && (
            <Alert type="error" showIcon role="alert" message={outcome === "limited" ? limitText : t("error.network")} />
          )}
          <div style={{ display: "flex", justifyContent: "flex-end" }}>
            <Button type="primary" htmlType="submit" disabled={sending}>
              {t("share.request.submit")}
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}
