// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect } from "react";
import { useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Alert, Skeleton } from "antd";
import { useLang, useT } from "../lib/i18n";
import { go } from "../lib/router";
import { queryKey } from "../lib/query";
import { useStore } from "../lib/store";
import { session } from "../lib/session";
import { statNode, statOrNull } from "../lib/files/api";
import { fileErrorText } from "../lib/files/fileErrors";
import { linkQuery, planNodeLink, type NodeLinkPlan } from "../lib/files/nodeLink";
import { editorEntry, editorPath, officeEdit } from "../lib/files/editor";
import type { Node } from "../lib/files/types";
import { filesURL, validID } from "../lib/files/route";
import Preview from "../components/files/Preview";
import NotFoundPage from "./NotFoundPage";

// NodeLink resolves /n/{nod}, the address share mails use. The decision is planNodeLink's: a file opens its
// preview in its folder, or on its own when the folder cannot be reached; a directory opens its parent with the
// directory selected. Anything the caller cannot reach is not found.
// node is the node the caller reached: an office file with tab=1 goes to its editor (oe1).
type Resolved = { kind: "missing" } | { kind: "plan"; plan: NodeLinkPlan; node: Node };

async function resolve(id: string): Promise<Resolved> {
  if (!validID("nod", id)) return { kind: "missing" };
  const node = await statOrNull(id);
  if (!node) return { kind: "missing" };
  // The parent is read only when there is one; a folder's root has none. null means the caller cannot reach it.
  const parent = node.parent_id === null ? null : await statNode(node.parent_id).catch(() => null);
  return { kind: "plan", plan: planNodeLink(node, parent), node };
}

export default function NodeLinkPage() {
  const { t } = useT();
  const lang = useLang();
  const loc = useLocation();
  const path = loc.pathname;
  // A notification's address asks for the file's comments; the request is kept through the redirect below.
  const details = new URLSearchParams(loc.search).get("details") === "comments";
  const linkID = path.split("/")[2] ?? "";
  const signedIn = useStore(session) !== null;
  const q = useQuery({ queryKey: queryKey("node-link", linkID), queryFn: () => resolve(linkID), enabled: signedIn });

  const plan = q.data?.kind === "plan" ? q.data.plan : null;
  // A link a script opened in a new tab (tab=1, decision 102) goes straight to the office editor when office editing is on,
  // full page, so the tab is the editor's own. Other links, and an office file with the editor off, take the folder view.
  const officeOn = useStore(officeEdit);
  const editorMode = q.data?.kind === "plan" && new URLSearchParams(loc.search).get("tab") === "1" ? editorEntry(q.data.node, officeOn) : null;
  const editorTo = editorMode && q.data?.kind === "plan" ? editorPath(q.data.node.id, editorMode, null, true) : null;
  // Derived from the plan of the current address, so a preview never outlives the link that opened it.
  const alone = plan?.kind === "alone" ? plan.node : null;
  useEffect(() => {
    if (editorTo !== null) {
      if (window.location.pathname === path) go(editorTo, { replace: true });
      return;
    }
    if (!plan) return;
    // The read can finish after the person has moved on (a sidebar click while the page is still drawn): then the
    // address is no longer this link's, and it is not moved back.
    if (window.location.pathname !== path) return;
    if (plan.kind === "folder") {
      go(filesURL(plan.folder, null), { replace: true });
    } else if (plan.kind !== "alone") {
      go(filesURL(plan.folder, plan.dir, linkQuery(plan, details)), { replace: true });
    }
  }, [plan, path, details, editorTo]);

  if (q.data?.kind === "missing" || !validID("nod", linkID)) {
    return <NotFoundPage nodeID={validID("nod", linkID) ? linkID : undefined} />;
  }
  if (q.isError) return <Alert type="error" showIcon role="alert" message={fileErrorText(q.error, lang)} />;
  if (alone) {
    return <Preview items={[alone]} index={0} onIndex={() => undefined} onClose={() => go("/files", { replace: true })} />;
  }
  return (
    <div role="status" aria-busy="true">
      <span className="sr-only">{t("app.loading")}</span>
      <Skeleton active paragraph={{ rows: 3 }} />
    </div>
  );
}

