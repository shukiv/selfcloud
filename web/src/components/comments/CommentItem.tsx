// SPDX-License-Identifier: AGPL-3.0-or-later

import { useId, useState, type CSSProperties } from "react";
import { Alert, Button, Input, Space, Tag } from "antd";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useLang } from "../../lib/i18n";
import { deleteComment, editComment, listReplies, resolveComment, type Comment } from "../../lib/comments/api";
import { useCT } from "../../lib/comments/i18n";
import {
  canDelete,
  canEdit,
  canReply,
  canResolve,
  checkBody,
  formatWhen,
  nextPage,
} from "../../lib/comments/logic";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import Confirm from "../Confirm";
import { faultMessage } from "./faults";
import Composer from "./Composer";
import { listIsStale, refreshAfterChange } from "./refresh";

// CommentItem is one comment, with its replies. The text is shown as text: it is a child of a bdi element, never HTML,
// and links in it are not made clickable. A reply has no reply of its own.
export default function CommentItem({
  nodeID,
  comment: c,
  role,
  isReply = false,
}: {
  nodeID: string;
  comment: Comment;
  role: string | null | undefined;
  isReply?: boolean;
}) {
  const { t } = useCT();
  const lang = useLang();
  const qc = useQueryClient();
  const uid = useId();
  const [replyOpen, setReplyOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editText, setEditText] = useState(c.body ?? "");
  const [editError, setEditError] = useState<string | null>(null);
  const [editBusy, setEditBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [repliesOpen, setRepliesOpen] = useState(false);

  // The replies past the first page, read when the person asks for them. The first page is in the list already.
  const replies = useInfiniteQuery({
    queryKey: queryKey("comments", nodeID, "replies", c.id),
    queryFn: ({ pageParam }) => listReplies(nodeID, c.id, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextPage,
    enabled: repliesOpen && !isReply,
    refetchOnMount: "always",
  });

  const refresh = () => refreshAfterChange(qc, nodeID);

  const author = c.mine ? t("comments.mine") : c.author_gone ? t("comments.former") : (c.author_name ?? "");
  const shownReplies = repliesOpen && replies.data ? replies.data.pages.flatMap((p) => p.items) : (c.replies ?? []);

  const saveEdit = async () => {
    const broke = checkBody(editText, []);
    if (broke) {
      setEditError(t(broke.key, broke.params));
      return;
    }
    setEditBusy(true);
    setEditError(null);
    const stale = sessionChanged();
    try {
      await editComment(c.id, editText);
      if (stale()) return;
      setEditing(false);
      await refresh();
    } catch (err) {
      // The text stays in the box, so nothing typed is lost. A refusal that makes the list out of date reads it again.
      if (stale()) return;
      setEditError(faultMessage(err, t, "comments.saveFailed"));
      if (listIsStale(err)) void refresh();
    } finally {
      setEditBusy(false);
    }
  };

  const doDelete = async () => {
    setDeleteBusy(true);
    setDeleteError(null);
    const stale = sessionChanged();
    try {
      await deleteComment(c.id);
      if (stale()) return;
      setConfirmDelete(false);
      await refresh();
    } catch (err) {
      if (stale()) return;
      // A comment that is gone, or a switch that is off, needs no retry: the confirm closes and the list is read again.
      if (listIsStale(err)) {
        setConfirmDelete(false);
        void refresh();
        return;
      }
      setDeleteError(faultMessage(err, t, "comments.failed"));
    } finally {
      setDeleteBusy(false);
    }
  };

  const toggleResolved = async () => {
    setActionError(null);
    const stale = sessionChanged();
    try {
      await resolveComment(c.id, !c.resolved);
      if (!stale()) await refresh();
    } catch (err) {
      if (stale()) return;
      setActionError(faultMessage(err, t, "comments.resolveFailed"));
      if (listIsStale(err)) void refresh();
    }
  };

  const showEdit = canEdit(c, role);
  const showDelete = canDelete(c, role);
  const showReply = canReply(c, role) && !isReply;
  const showResolve = canResolve(c, role) && !isReply;

  return (
    <article
      data-testid={isReply ? "comment-reply" : "comment"}
      data-deleted={c.deleted ? "true" : undefined}
      style={{ ...ROW, ...(isReply ? REPLY : null) }}
    >
      <header style={{ display: "flex", flexWrap: "wrap", gap: "4px 12px", alignItems: "baseline", fontSize: 13 }}>
        {author !== "" && (
          <strong>
            <bdi>{author}</bdi>
          </strong>
        )}
        <time dateTime={c.created_at} style={{ color: "var(--ant-color-text-secondary)" }}>
          {formatWhen(c.created_at, lang)}
        </time>
        {c.edited_at && !c.deleted && <span style={{ color: "var(--ant-color-text-secondary)" }}>{t("comments.edited")}</span>}
        {c.resolved && !isReply && <Tag color="green">{t("comments.resolved")}</Tag>}
      </header>

      {c.deleted ? (
        <p style={{ margin: "6px 0 0", fontStyle: "italic", color: "var(--ant-color-text-secondary)" }}>{t("comments.deleted")}</p>
      ) : editing ? (
        <div style={{ display: "grid", gap: 8, marginBlockStart: 6 }}>
          <Input.TextArea
            rows={3}
            value={editText}
            disabled={editBusy}
            aria-label={t("comments.editLabel")}
            onChange={(e) => setEditText(e.target.value)}
            status={editError ? "error" : undefined}
          />
          {editError && <Alert type="error" showIcon role="alert" message={editError} />}
          <Space wrap>
            <Button type="primary" loading={editBusy} onClick={() => void saveEdit()}>
              {t("comments.save")}
            </Button>
            <Button
              disabled={editBusy}
              onClick={() => {
                setEditing(false);
                setEditError(null);
                setEditText(c.body ?? "");
              }}
            >
              {t("comments.cancel")}
            </Button>
          </Space>
        </div>
      ) : (
        <p style={BODY}>
          <bdi>{c.body ?? ""}</bdi>
        </p>
      )}

      {!c.deleted && !editing && (showReply || showEdit || showDelete || showResolve) && (
        <Space wrap size={[8, 4]} style={{ marginBlockStart: 4 }}>
          {showReply && (
            <Button type="link" onClick={() => setReplyOpen((o) => !o)} aria-expanded={replyOpen}>
              {t("comments.reply")}
            </Button>
          )}
          {showEdit && (
            <Button
              type="link"
              onClick={() => {
                setEditText(c.body ?? "");
                setEditError(null);
                setEditing(true);
              }}
            >
              {t("comments.edit")}
            </Button>
          )}
          {showResolve && (
            <Button type="link" onClick={() => void toggleResolved()}>
              {c.resolved ? t("comments.reopen") : t("comments.resolve")}
            </Button>
          )}
          {showDelete && (
            <Button type="link" danger onClick={() => setConfirmDelete(true)}>
              {t("comments.delete")}
            </Button>
          )}
        </Space>
      )}
      {actionError && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBlockStart: 6 }} />}

      {replyOpen && (
        <div style={{ marginBlockStart: 8 }}>
          <Composer
            nodeID={nodeID}
            parentID={c.id}
            label={c.mine ? t("comments.newLabel") : t("comments.replyLabel", { name: author })}
            submit={t("comments.postReply")}
            autoFocus
            onDone={() => setReplyOpen(false)}
            onCancel={() => setReplyOpen(false)}
          />
        </div>
      )}

      {repliesOpen && replies.isPending && (
        <p role="status" style={{ margin: "8px 0 0", fontSize: 13 }}>
          {t("comments.loading")}
        </p>
      )}
      {shownReplies.length > 0 && (
        <div style={{ marginBlockStart: 8, paddingInlineStart: 16, borderInlineStart: "2px solid var(--ant-color-border)" }}>
          {shownReplies.map((r) => (
            <CommentItem key={r.id} nodeID={nodeID} comment={r} role={role} isReply />
          ))}
        </div>
      )}
      {repliesOpen && replies.isError && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={t("comments.failed")}
          style={{ marginBlockStart: 8 }}
          action={
            <Button size="small" onClick={() => void replies.refetch()}>
              {t("comments.retry")}
            </Button>
          }
        />
      )}
      {!isReply && c.replies_more === true && !repliesOpen && (
        <Button type="link" onClick={() => setRepliesOpen(true)}>
          {t("comments.showMoreReplies")}
        </Button>
      )}
      {!isReply && repliesOpen && replies.hasNextPage && (
        <Button type="link" loading={replies.isFetchingNextPage} onClick={() => void replies.fetchNextPage()}>
          {t("comments.loadMoreReplies")}
        </Button>
      )}

      {confirmDelete && (
        <Confirm
          id={`${uid}-delete`}
          title={t("comments.confirmDeleteTitle")}
          message={t("comments.confirmDeleteBody")}
          action={t("comments.confirmDelete")}
          danger
          busy={deleteBusy}
          error={deleteError}
          onconfirm={() => void doDelete()}
          onclose={() => {
            setConfirmDelete(false);
            setDeleteError(null);
          }}
        />
      )}
    </article>
  );
}

const ROW: CSSProperties = { display: "grid", gap: 4, padding: "12px 0", borderBlockEnd: "1px solid var(--ant-color-split)" };
const REPLY: CSSProperties = { paddingBlock: "8px", borderBlockEnd: "none" };
const BODY: CSSProperties = { margin: "6px 0 0", whiteSpace: "pre-wrap", overflowWrap: "anywhere" };
