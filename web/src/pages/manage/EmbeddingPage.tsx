// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { Alert, App, Button, Input, Modal, Popconfirm, Skeleton } from "antd";
import { Plus, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import Listing from "../../components/files/Listing";
import IconButton from "../../components/IconButton";
import { OwnerOnly, useAccountID, useOnce } from "../../components/manage/OwnerPage";
import { useLang, useT } from "../../lib/i18n";
import { loadEmbed, useET } from "../../lib/embed/i18n";
import { useFlight } from "../../lib/flight";
import { failureText } from "../../lib/manage/failure";
import { listEmbedOrigins, replaceEmbedOrigins } from "../../lib/manage/api";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import { useStore } from "../../lib/store";
import { brand } from "../../lib/brand";
import { ApiError } from "../../lib/apiError";

// Embedding: the sites that may show this account's pages inside a frame of their own. The owner reads the whole list
// and replaces it whole: the server normalises, deduplicates and sorts what it is sent, and answers with the list it
// keeps, so the page shows that answer rather than its own guess. The texts are the embed catalogue's, loaded with the
// page (decision 214); the page waits for them before it draws its table.
function EmbeddingView() {
  const { t } = useT();
  const { t: et, ready, failed } = useET();
  const lang = useLang();
  const qc = useQueryClient();
  const account = useAccountID();
  const name = useStore(brand).name;
  const [adding, setAdding] = useState(false);
  const [value, setValue] = useState("");
  const [addError, setAddError] = useState<string | null>(null);
  const [pageError, setPageError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const flight = useFlight();
  const once = useOnce();
  const { message } = App.useApp();

  const key = queryKey("manage", account, "embed");
  const q = useQuery({
    queryKey: key,
    queryFn: () => listEmbedOrigins(account),
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const origins = q.data?.origins ?? [];
  const loaded = q.data !== undefined;

  // The title is the embed catalogue's own, so it is set once that catalogue has arrived (useManageTitle reads the main one).
  useEffect(() => {
    if (ready) document.title = `${et("embed.manage.title")} · ${name}`;
  }, [ready, et, name]);

  const openAdd = () => {
    setValue("");
    setAddError(null);
    setAdding(true);
  };
  // cancelAdd closes the dialog at once, busy or not: a write on its way is aborted, and the list is read again (fu4).
  const cancelAdd = () => {
    if (flight.abort()) {
      void message.warning(t("common.cancelledMaybe"));
      void q.refetch();
    }
    setAddError(null);
    setAdding(false);
  };

  const submitAdd = () =>
    once(async () => {
      const v = value.trim();
      if (v === "") return;
      const stale = sessionChanged();
      const signal = flight.start();
      setBusy(true);
      setAddError(null);
      try {
        const answer = await replaceEmbedOrigins(account, Array.from(new Set([...origins, v])), signal);
        if (stale()) return;
        qc.setQueryData(key, { origins: answer.origins });
        setAdding(false);
        setValue("");
      } catch (err) {
        if (stale() || signal.aborted) return;
        // A refused origin is named by the server itself: that sentence goes under the field, and the dialog stays open.
        if (err instanceof ApiError && err.status === 400 && err.serverMessage !== "") setAddError(err.serverMessage);
        else setAddError(failureText(err, lang));
      } finally {
        flight.end();
        setBusy(false);
      }
    });

  const remove = (origin: string) =>
    once(async () => {
      const stale = sessionChanged();
      const signal = flight.start();
      setBusy(true);
      setPageError(null);
      try {
        const answer = await replaceEmbedOrigins(account, origins.filter((o) => o !== origin), signal);
        if (stale()) return;
        qc.setQueryData(key, { origins: answer.origins });
      } catch (err) {
        if (stale() || signal.aborted) return;
        setPageError(failureText(err, lang));
      } finally {
        flight.end();
        setBusy(false);
      }
    });

  if (failed) {
    return (
      <Alert
        type="error"
        showIcon
        role="alert"
        message={t("app.crash.title")}
        action={<Button size="small" onClick={() => void loadEmbed(lang)}>{t("upload.retry")}</Button>}
      />
    );
  }
  if (!ready) return <Skeleton active paragraph={{ rows: 4 }} />;

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {et("embed.manage.title")}
        </h1>
        <Button type="primary" icon={<Plus size={16} aria-hidden="true" focusable="false" />} disabled={busy} onClick={openAdd}>
          {et("embed.manage.add")}
        </Button>
      </div>

      <p style={{ margin: "0 0 8px", color: "var(--ant-color-text-secondary)" }}>{et("embed.manage.intro")}</p>

      {q.isError && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={failureText(q.error, lang)}
          action={<Button size="small" onClick={() => void q.refetch()}>{t("files.retry")}</Button>}
          style={{ marginBottom: 12 }}
        />
      )}
      {pageError !== null && <Alert type="error" showIcon role="alert" message={pageError} style={{ marginBottom: 12 }} />}

      {!loaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {loaded && (
        <Listing
          label={et("embed.manage.title")}
          items={origins.map((o) => ({ id: o }))}
          rowLabel={(r) => r.id}
          columns={[
            { key: "site", title: et("embed.manage.col.site"), render: (r) => <bdi dir="ltr">{r.id}</bdi> },
            {
              key: "actions",
              title: t("storage.actions"),
              align: "end",
              width: 80,
              render: (r) => (
                <Popconfirm
                  title={et("embed.manage.removeConfirm", { origin: r.id })}
                  okText={et("embed.manage.remove")}
                  cancelText={t("common.cancel")}
                  okButtonProps={{ danger: true }}
                  onConfirm={() => void remove(r.id)}
                >
                  <IconButton
                    label={`${et("embed.manage.remove")}: ${r.id}`}
                    icon={<Trash2 size={18} aria-hidden="true" focusable="false" />}
                    danger
                    disabled={busy}
                  />
                </Popconfirm>
              ),
            },
          ]}
          empty={{
            title: et("embed.manage.empty"),
            description: et("embed.manage.emptyDescription"),
            action: { label: et("embed.manage.add"), onClick: openAdd },
          }}
        />
      )}

      {adding && (
        <Modal open title={et("embed.manage.add.title")} onCancel={cancelAdd} maskClosable={false} footer={null} destroyOnClose>
          <form
            id="embed-add"
            onSubmit={(e) => {
              e.preventDefault();
              void submitAdd();
            }}
            style={{ display: "grid", gap: 16 }}
            noValidate
          >
            <div style={{ display: "grid", gap: 4 }}>
              <label htmlFor="embed-add-origin">{et("embed.manage.add.label")}</label>
              <Input
                id="embed-add-origin"
                autoComplete="off"
                placeholder={et("embed.manage.add.placeholder")}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                autoFocus
              />
            </div>
            {addError && <Alert type="error" showIcon role="alert" message={addError} />}
            <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" }}>
              <Button onClick={cancelAdd}>{t("common.cancel")}</Button>
              <Button type="primary" htmlType="submit" disabled={busy || value.trim() === ""}>
                {et("embed.manage.add.ok")}
              </Button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}

export default function EmbeddingPage() {
  return (
    <OwnerOnly section="embedding">
      <EmbeddingView />
    </OwnerOnly>
  );
}