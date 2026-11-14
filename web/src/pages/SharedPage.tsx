// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Skeleton, Tag } from "antd";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import Listing, { type ListColumn } from "../components/files/Listing";
import Icon from "../components/files/Icon";
import DriveName, { openDrive } from "../components/drive/DriveName";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session } from "../lib/session";
import { queryKey } from "../lib/query";
import { fileErrorText } from "../lib/files/fileErrors";
import { errorMessage, type Message } from "../lib/errors";
import { formatDate } from "../lib/files/format";
import { kindOf } from "../lib/files/kind";
import { go } from "../lib/router";
import { proveEmail, sharedExternal, sharedWithMe, type ExternalShared, type SharedNode } from "../lib/sharing/api";
import type { Page } from "../lib/files/types";
import { useDriveText } from "../lib/drive/driveText";
import { useLoadMoreSentinel } from "../lib/files/loadMoreSentinel";
import DriveTextFailure from "../components/drive/DriveTextFailure";

// ExternalShares is the "From other accounts" section: the items people in other accounts shared with the caller's proved
// address. While the address is not proved it asks for the proof mail instead of the items; once proved it lists them with
// their sharer and account, and an item opens at /x/{nod}. A failed proof ask shows its own message and keeps the prompt.
function ExternalShares({ ready }: { ready: boolean }) {
  const { t, tp } = useT();
  const lang = useLang();
  const me = useStore(session);
  const qc = useQueryClient();
  const key = queryKey("shared-external");
  const [note, setNote] = useState<string | null>(null);
  const [proofError, setProofError] = useState<Message | null>(null);
  const [busy, setBusy] = useState(false);

  const q = useQuery({
    queryKey: key,
    queryFn: () => sharedExternal(),
    enabled: me !== null && me.accountExternalSharing === true,
    refetchOnMount: "always",
  });

  const proved = q.data?.email_proved === true || me?.emailProved === true;
  const items = q.data?.items ?? [];

  const sendProof = async () => {
    setBusy(true);
    setNote(null);
    setProofError(null);
    try {
      const res = await proveEmail();
      if (res.proved) {
        await qc.invalidateQueries({ queryKey: key });
      } else if (res.sent) {
        setNote(t("shared.external.proveSent", { email: me?.user.email ?? "" }));
      } else {
        setNote(t("shared.external.proveFailed"));
      }
    } catch (err) {
      setProofError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const columns: ListColumn<ExternalShared>[] = [
    { key: "name", title: t("files.col.name"), render: (n) => <bdi>{n.name}</bdi> },
    { key: "by", title: t("drive.col.sharedBy"), secondary: true, render: (n) => t("shared.external.by", { name: n.shared_by_name, account: n.account_name }) },
    { key: "when", title: t("files.col.modified"), secondary: true, render: (n) => <time dateTime={n.modified_at}>{formatDate(n.modified_at, lang)}</time> },
  ];

  // A proved address waits for its items and the table's words before the section appears, so the region is found only
  // once its rows are there; until then a skeleton stands in without the heading.
  if (proved && (q.isPending || !ready)) {
    return <Skeleton active />;
  }
  return (
    <section style={{ marginTop: 32 }}>
      <h2 id="shared-external-title" style={{ fontSize: 18, fontWeight: 600, margin: "0 0 12px" }}>
        {t("shared.external.title")}
      </h2>
      {!proved ? (
        <div style={{ display: "grid", gap: 12 }}>
          <Alert type="info" showIcon message={t("shared.external.prove")} />
          <div>
            <Button onClick={() => void sendProof()} loading={busy}>
              {t("shared.external.proveSend")}
            </Button>
          </div>
          {note && (
            <p role="status" style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>
              {note}
            </p>
          )}
          {proofError && (
            <Alert type="error" showIcon role="alert" message={proofError.plural !== undefined ? tp(proofError.key, proofError.plural) : t(proofError.key)} />
          )}
        </div>
      ) : (
        <Listing<ExternalShared>
          label={t("shared.external.title")}
          items={items}
          rowLabel={(n) => n.name}
          columns={columns}
          icon={(n) => <Icon kind={kindOf({ kind: n.kind, mime_type: n.mime ?? "" })} size={24} />}
          onOpen={(n) => go(`/x/${n.id}`)}
          empty={{ title: t("shared.external.empty"), className: "empty" }}
        />
      )}
    </section>
  );
}

// Shared with me: the items other people shared with the caller, each at the role it has, on the one table (tl4, decision 137), in
// the order the server gives them, newest share first. An item opens through its address (/n/{nod}), like any link a share mail
// carries. A failed refresh or a failed load-more keeps the list on screen and shows the failure above it.
export default function SharedPage() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const signedIn = me !== null;
  const drive = useDriveText();
  const driveReady = drive.ready;

  const q = useInfiniteQuery({
    queryKey: queryKey("shared"),
    queryFn: ({ pageParam }: { pageParam: string | undefined }) => sharedWithMe(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<SharedNode>) => last.next_cursor,
    enabled: signedIn,
    refetchOnMount: "always",
  });

  const items = useMemo<SharedNode[]>(() => (q.data?.pages ?? []).flatMap((p) => p.items), [q.data]);
  const hasMore = q.hasNextPage === true;
  const sentinel = useLoadMoreSentinel(hasMore, q.isFetchingNextPage, q.isFetchNextPageError, () => void q.fetchNextPage());
  const hasData = q.data !== undefined;

  useEffect(() => {
    document.title = `${t("nav.shared")} · ${brandName}`;
  }, [t, brandName]);

  // ownerText names the owner: "You" when it is the caller, otherwise the name the API gives, or nothing when it gives none.
  const ownerText = (n: SharedNode): string => (n.owner_id !== undefined && me !== null && n.owner_id === me.user.id ? t("files.details.you") : (n.owner_name ?? ""));

  const failure = q.isError ? fileErrorText(q.error, lang) : null;

  const columns: ListColumn<SharedNode>[] = [
    { key: "name", title: t("files.col.name"), render: (n) => <DriveName node={n} /> },
    { key: "by", title: t("drive.col.sharedBy"), secondary: true, render: (n) => ownerText(n) },
    { key: "role", title: t("drive.col.yourRole"), secondary: true, render: (n) => <Tag>{t(`role.${n.role ?? "viewer"}`)}</Tag> },
    {
      key: "when",
      title: t("drive.col.sharedOn"),
      secondary: true,
      ariaSort: "descending",
      render: (n) => (n.shared_at ? <time dateTime={n.shared_at}>{formatDate(n.shared_at, lang)}</time> : ""),
    },
  ];

  return (
    <div>
      <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>
        {t("nav.shared")}
      </h1>

      {failure !== null && <Alert type={hasData ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {drive.failed && <DriveTextFailure retry={drive.retry} />}
      {((!driveReady && !drive.failed) || (!hasData && q.isPending && signedIn)) && <Skeleton active />}

      {driveReady && hasData && (items.length > 0 || failure === null) && (
        <div className="shared">
          <Listing<SharedNode>
            label={t("nav.shared")}
            items={items}
            rowLabel={(n) => n.name}
            columns={columns}
            icon={(n) => <Icon kind={kindOf(n)} size={24} node={n} />}
            menu={(n) => [{ key: "open", label: t("files.act.open"), onSelect: () => openDrive(n) }]}
            onOpen={(n) => openDrive(n)}
            empty={{ title: t("drive.empty.shared"), description: t("shared.empty"), className: "empty" }}
          />
        </div>
      )}

      {hasMore && <div ref={sentinel} aria-hidden="true" />}
      {hasMore && (
        <div style={{ display: "flex", justifyContent: "center", padding: "16px 0" }}>
          <Button onClick={() => void q.fetchNextPage()} loading={q.isFetchingNextPage}>
            {t("files.loadMore")}
          </Button>
        </div>
      )}

      {me?.accountExternalSharing === true && <ExternalShares ready={driveReady} />}
    </div>
  );
}
