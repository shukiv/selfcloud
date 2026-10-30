// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense } from "react";
import { useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { session } from "../lib/session";

// The access request form is loaded only when a signed-in person needs it.
const RequestAccess = lazy(() => import("../components/share/RequestAccess"));

// NotFoundPage is the view for an address with no page, or one the person may not open. For a signed-in person
// on an item (nodeID), it offers to ask the people who manage the item for access. The form is keyed on the item,
// so an answer typed for one item never carries over to another.
export default function NotFoundPage({ nodeID }: { nodeID?: string }) {
  const { t } = useT();
  const signedIn = useStore(session) !== null;
  return (
    <div>
      <h1 style={{ fontSize: 24 }}>{t("app.notFound.title")}</h1>
      <p>{t("app.notFound.body")}</p>
      {nodeID && signedIn && (
        <Suspense fallback={null}>
          <RequestAccess key={nodeID} nodeID={nodeID} />
        </Suspense>
      )}
    </div>
  );
}
