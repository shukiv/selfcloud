// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Tabs } from "antd";
import { useInfiniteQuery } from "@tanstack/react-query";
import { listComments, listViewers, type Comment, type Page, type Viewer } from "../../lib/comments/api";
import { useCT } from "../../lib/comments/i18n";
import { commentFault, nextPage } from "../../lib/comments/logic";
import { queryKey } from "../../lib/query";
import ActivityList from "./ActivityList";
import CommentsPanel from "./CommentsPanel";
import ViewersList from "./ViewersList";
import { SectionFailed } from "../SectionBoundary";

// CommentsTabs is the lower part of the file details: the comments, the activity and, for a manager while the account
// allows it, who viewed. It is loaded as a chunk of its own, with the panels.
//
// The comments tab is shown unless the account's comment switch is off (the server answers forbidden with reason
// comments_off, and that is the signal to hide the tab, not an error). The who-viewed tab is shown only once the list
// answers: a switch that is off answers forbidden with reason who_viewed_off, and the tab then stays hidden.
export default function CommentsTabs({ nodeID, role }: { nodeID: string; role: string | null | undefined }) {
  const { t, ready, failed } = useCT();
  const isManager = role === "manager";

  const comments = useInfiniteQuery({
    queryKey: queryKey("comments", nodeID),
    queryFn: ({ pageParam }) => listComments(nodeID, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<Comment>) => nextPage(last),
    refetchOnMount: "always",
  });

  const viewers = useInfiniteQuery({
    queryKey: queryKey("viewers", nodeID),
    queryFn: ({ pageParam }) => listViewers(nodeID, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: Page<Viewer>) => nextPage(last),
    enabled: isManager,
    retry: false,
    refetchOnMount: "always",
  });

  const commentsOff = comments.isError && commentFault(comments.error) === "off";
  const viewersOff = viewers.isError && commentFault(viewers.error) === "viewers_off";
  const showViewers = isManager && !viewersOff && !viewers.isPending && (viewers.isSuccess || viewers.isError);

  const [picked, setPicked] = useState<string>("comments");
  const items = [
    ...(commentsOff ? [] : [{ key: "comments", label: t("comments.tab"), children: <CommentsPanel nodeID={nodeID} role={role} q={comments} /> }]),
    { key: "activity", label: t("activity.tab"), children: <ActivityList nodeID={nodeID} /> },
    ...(showViewers ? [{ key: "viewers", label: t("viewers.tab"), children: <ViewersList q={viewers} /> }] : []),
  ];
  const active = items.some((i) => i.key === picked) ? picked : (items[0]?.key ?? "activity");

  // The texts of this section did not load: the message is the main catalogue's, which is always there.
  if (failed) return <div style={{ marginBlockStart: 24 }}><SectionFailed /></div>;
  if (!ready) return null;
  return (
    <section className="comments-panel" aria-label={t("comments.tab")} style={{ marginBlockStart: 24, borderBlockStart: "1px solid var(--ant-color-split)", paddingBlockStart: 16 }}>
      {commentsOff && (
        <p role="note" style={{ margin: "0 0 12px", fontSize: 13, color: "var(--ant-color-text-secondary)" }}>
          {t("comments.off")}
        </p>
      )}
      <Tabs activeKey={active} onChange={setPicked} items={items} />
    </section>
  );
}
