// SPDX-License-Identifier: AGPL-3.0-or-later

// BookmarksDialog is the lazy list of the file's bookmarks (decision 218): a table of Time and Label, each row's Go to (seek the
// element and close, keeping play/pause), Rename (an inline input of at most LABEL_MAX characters, Enter saves, Escape cancels)
// and Delete; the empty state with its add action; and a failed read's Retry. The footer keeps Add a bookmark here (disabled when
// the file is full or there is no element) and Close. The hook owns the list and its messages; the dialog draws it and runs the
// row actions, keeping a rename open if its save is refused.
import { useState } from "react";
import { App, Button, Input, Modal, Typography } from "antd";
import { Pencil, Play, Trash2 } from "lucide-react";
import { useMT } from "../../lib/media/i18n";
import { formatTime } from "../../lib/files/mediaKeys";
import { LABEL_MAX, type Marks } from "../../lib/files/bookmarks";
import type { Bookmark } from "../../lib/files/api";
import Listing, { type ListAction, type ListColumn } from "./Listing";

interface Props {
  name: string;
  marks: Marks;
  media: HTMLMediaElement | null;
  readFailed: boolean;
  onRelabel: (bmk: string, label: string) => Promise<Bookmark>;
  onRemove: (bmk: string) => Promise<void>;
  onRetry: () => void;
  onClose: () => void;
}

export default function BookmarksDialog({ name, marks, media, readFailed, onRelabel, onRemove, onRetry, onClose }: Props) {
  const mt = useMT();
  const { message } = App.useApp();
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState("");

  const startRename = (b: Bookmark) => {
    setEditing(b.id);
    setDraft(b.label);
  };
  const saveRename = () => {
    if (editing === null) return;
    const id = editing;
    void onRelabel(id, draft)
      .then(() => setEditing(null))
      .catch(() => {});
  };

  const columns: ListColumn<Bookmark>[] = [
    {
      key: "time",
      title: mt.t("media.bookmarks.time"),
      width: 96,
      render: (b) => <span dir="ltr">{formatTime(b.position_seconds)}</span>,
    },
    {
      key: "label",
      title: mt.t("media.bookmarks.label"),
      render: (b) =>
        editing === b.id ? (
          <Input
            autoFocus
            maxLength={LABEL_MAX}
            value={draft}
            data-testid="bookmark-rename"
            onChange={(e) => setDraft(e.target.value)}
            onPressEnter={saveRename}
            onKeyDown={(e) => {
              if (e.key === "Escape") setEditing(null);
            }}
          />
        ) : b.label ? (
          <bdi dir="auto">{b.label}</bdi>
        ) : (
          <Typography.Text type="secondary">{mt.t("media.bookmarks.noLabel")}</Typography.Text>
        ),
    },
  ];

  const actions = (b: Bookmark): ListAction[] => [
    {
      key: "goto",
      label: mt.t("media.bookmarks.goto"),
      icon: <Play size={18} aria-hidden="true" focusable="false" />,
      run: () => {
        if (media) media.currentTime = b.position_seconds;
        onClose();
      },
    },
    { key: "rename", label: mt.t("media.bookmarks.rename"), icon: <Pencil size={18} aria-hidden="true" focusable="false" />, run: () => startRename(b) },
    {
      key: "delete",
      label: mt.t("media.bookmarks.delete"),
      icon: <Trash2 size={18} aria-hidden="true" focusable="false" />,
      danger: true,
      run: () => {
        void onRemove(b.id).catch(() => message.error(mt.t("media.bookmarks.deleteFailed")));
      },
    },
  ];

  return (
    <Modal
      open
      title={mt.t("media.bookmarks.title")}
      maskClosable={false}
      onCancel={onClose}
      width={560}
      footer={[
        <Button key="add" disabled={marks.full || !media} onClick={() => marks.add()}>
          {mt.t("media.bookmarks.addHere")}
        </Button>,
        <Button key="close" type="primary" onClick={onClose}>
          {mt.t("media.bookmarks.close")}
        </Button>,
      ]}
    >
      <div className="media-bookmarks-name">
        <bdi dir="auto">{name}</bdi>
      </div>
      {readFailed ? (
        <div className="media-bookmarks-failed">
          <Typography.Text>{mt.t("media.bookmarks.readFailed")}</Typography.Text>
          <Button onClick={onRetry}>{mt.t("media.bookmarks.retry")}</Button>
        </div>
      ) : (
        <Listing<Bookmark>
          items={marks.items}
          label={mt.t("media.bookmarks.title")}
          rowLabel={(b) => b.label || formatTime(b.position_seconds)}
          columns={columns}
          actions={actions}
          rowTestID={() => "bookmark-row"}
          empty={{
            title: mt.t("media.bookmarks.emptyTitle"),
            description: mt.t("media.bookmarks.emptyBody"),
            action: { label: mt.t("media.bookmarks.addHere"), onClick: () => marks.add() },
          }}
          stickyTop={0}
        />
      )}
    </Modal>
  );
}
