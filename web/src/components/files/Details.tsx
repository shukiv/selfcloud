// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useState, type CSSProperties } from "react";
import { App, Button, Drawer } from "antd";
import { api } from "../../lib/api";
import { useFlight } from "../../lib/flight";
import { ApiError } from "../../lib/apiError";
import Confirm from "../Confirm";
import { useT, useLang } from "../../lib/i18n";
import { session } from "../../lib/session";
import { useStore } from "../../lib/store";
import { formatBytes, formatDate } from "../../lib/files/format";
import { detailsPersonLabel } from "../../lib/files/people";
import { kindOf } from "../../lib/files/kind";
import type { Node } from "../../lib/files/types";
import Icon from "./Icon";
import { Copy } from "lucide-react";
import IconButton from "../IconButton";
import Appearance from "./Appearance";
import Labels from "./Labels";
import SectionBoundary from "../SectionBoundary";

// The comments, activity and who-viewed tabs are a chunk of their own: they are not needed for the first screen.
const CommentsTabs = lazy(() => import("../comments/CommentsTabs"));

// Details is the side drawer for one node. The Share button shows only when the caller may share the node;
// "Who has access" always shows, and both open the share dialog. onChanged is told of a colour or description saved.
export default function Details({
  node,
  canVersions,
  canShare,
  onClose,
  onVersions,
  onShare,
  onChanged,
}: {
  node: Node;
  canVersions: boolean;
  canShare: boolean;
  onClose: () => void;
  onVersions: () => void;
  onShare: () => void;
  onChanged?: (next: Node) => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const me = useStore(session);
  const [copied, setCopied] = useState<"idle" | "done" | "failed">("idle");
  // Ending an editing session (a manager's break of the lock) is confirmed first; the confirmation says edits may be lost.
  const [ending, setEnding] = useState(false);
  const [endBusy, setEndBusy] = useState(false);
  const [endError, setEndError] = useState<string | null>(null);
  // The unlock on its way is the request the confirm cancels (fu4, decision 76's rule for writes).
  const request = useFlight();
  const { message } = App.useApp();
  const cancelEnd = () => {
    if (request.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      // The unlock may have gone through: the file is read again, so the drawer does not show it as still being edited (fu4 review 3).
      void api<Node>("GET", `/api/v1/nodes/${node.id}`).then((fresh) => onChanged?.(fresh), () => undefined);
    }
    setEndError(null);
    setEnding(false);
  };
  async function endSession() {
    const signal = request.start();
    setEndBusy(true);
    setEndError(null);
    try {
      await api("POST", `/api/v1/nodes/${node.id}/unlock`, { signal });
      setEnding(false);
      onChanged?.({ ...node, editing: false, editing_by: undefined });
    } catch (err) {
      // Named by status: 409 is a file no editor holds any more, 404 a file that is gone. The server's text is not shown.
      if (signal.aborted) return;
      const status = err instanceof ApiError ? err.status : 0;
      setEndError(status === 409 ? t("files.editing.gone") : status === 404 ? t("files.editor.notFound") : t("files.editing.failed"));
    } finally {
      request.end();
      setEndBusy(false);
    }
  }

  const who = (id: string | undefined, name: string | undefined): string | null => detailsPersonLabel(id, name, me, t);

  async function copyHash() {
    try {
      await navigator.clipboard.writeText(node.sha256 ?? "");
      setCopied("done");
    } catch {
      setCopied("failed");
    }
  }

  return (
    <Drawer
      open
      maskClosable={false}
      width="min(420px, 100vw)"
      title={
        <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
          <Icon kind={kindOf(node)} size={24} colour={node.color} node={node} />
          <bdi>{node.name}</bdi>
        </span>
      }
      onClose={onClose}
      data-testid="details"
    >
      <dl style={{ display: "grid", gridTemplateColumns: "max-content minmax(0, 1fr)", gap: "8px 16px", margin: 0 }}>
        {node.kind === "file" && (
          <>
            <dt>{t("files.details.size")}</dt>
            <dd style={DD}>{formatBytes(node.size, lang)}</dd>
            <dt>{t("files.details.type")}</dt>
            <dd style={DD}>
              <bdi>{node.mime_type ?? ""}</bdi>
            </dd>
          </>
        )}
        <dt>{t("files.details.modified")}</dt>
        <dd style={DD}>{formatDate(node.modified_at, lang)}</dd>
        <dt>{t("files.details.created")}</dt>
        <dd style={DD}>{formatDate(node.created_at, lang)}</dd>
        {who(node.created_by, node.created_by_name) && (
          <>
            <dt>{t("files.details.createdBy")}</dt>
            <dd style={DD}>
              <bdi>{who(node.created_by, node.created_by_name)}</bdi>
            </dd>
          </>
        )}
        {who(node.owner_id, node.owner_name) && (
          <>
            <dt>{t("files.details.owner")}</dt>
            <dd style={DD}>
              <bdi>{who(node.owner_id, node.owner_name)}</bdi>
            </dd>
          </>
        )}
        {node.editing && (
          <>
            <dt>{t("files.details.editing")}</dt>
            <dd style={DD}>
              <bdi>{node.editing_by || t("files.details.editingUnnamed")}</bdi>
            </dd>
          </>
        )}
        <dt>{t("files.details.role")}</dt>
        <dd style={DD}>{node.role ? t(`role.${node.role}`) : ""}</dd>
        {node.sha256 && (
          <>
            <dt>{t("files.details.sha256")}</dt>
            <dd style={DD}>
              <code style={{ display: "block", overflowWrap: "anywhere", marginBlockEnd: 8 }}>{node.sha256}</code>
              <IconButton label={`${t("files.details.copy")}: ${node.name}`} tip={t("files.details.copy")} onClick={copyHash} icon={<Copy size={18} aria-hidden="true" focusable="false" />} />{" "}
              <span role="status">
                {copied === "done" ? t("files.details.copied") : copied === "failed" ? t("files.details.copyFailed") : ""}
              </span>
            </dd>
          </>
        )}
      </dl>
      {node.editing && node.role === "manager" && (
        <Button block danger style={{ marginBlockStart: 16 }} onClick={() => setEnding(true)}>
          {t("files.editing.end")}
        </Button>
      )}
      {ending && (
        <Confirm
          id="end-editing"
          title={t("files.editing.endTitle")}
          message={t("files.editing.endText")}
          action={t("files.editing.endAction")}
          danger
          busy={endBusy}
          error={endError}
          onconfirm={() => void endSession()}
          onclose={() => {
            setEnding(false);
            setEndError(null);
          }}
          oncancel={cancelEnd}
        />
      )}
      {node.kind === "shortcut" && node.unavailable && (
        <p role="note" style={{ marginBlockStart: 16 }}>
          {t("shortcut.unavailableBody")}
        </p>
      )}
      <Appearance key={node.id} node={node} onChanged={onChanged} />
      <Labels key={`labels-${node.id}`} node={node} />
      {node.kind === "file" && canVersions && (
        <Button block style={{ marginBlockStart: 16 }} onClick={onVersions}>
          {t("files.details.versions")}
        </Button>
      )}
      <div style={{ display: "grid", gap: 8, marginBlockStart: 16 }}>
        {canShare && (
          <Button type="primary" block onClick={onShare}>
            {t("share.open")}
          </Button>
        )}
        <Button block onClick={onShare}>
          {t("share.whoHas")}
        </Button>
      </div>
      {node.kind !== "shortcut" && (
        // A chunk that does not load leaves the rest of the drawer usable: the boundary is around the tabs only.
        <SectionBoundary>
          <Suspense fallback={null}>
            <CommentsTabs nodeID={node.id} role={node.role} />
          </Suspense>
        </SectionBoundary>
      )}
    </Drawer>
  );
}

const DD: CSSProperties = { margin: 0, minWidth: 0, overflowWrap: "anywhere" };
