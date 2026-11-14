// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { useLocation } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import PublicBrowse, { sessionSource } from "../components/share/PublicBrowse";
import EmptyState from "../components/EmptyState";
import { useT } from "../lib/i18n";
import { queryKey } from "../lib/query";
import type { ExternalShared } from "../lib/sharing/api";

// ITEM_ID is the shape of a node's ID (nod_ and 26 lowercase Crockford base32 characters, internal/id): an address that
// is not one reads nothing.
const ITEM_ID = /^nod_[0-9a-hjkmnp-tv-z]{26}$/;

// ExternalItemPage opens an item a person in another account shared with the caller, at /x/{nod}. It reads through the
// signed-in source (the drive's own calls, no visitor credential), and defers a file's link to the click that needs it.
// The byline under the name — who shared it and from which account — comes only from the shared-external answer the
// Shared with me page already cached; nothing is asked for it here. An item the caller cannot reach shows the gone state.
export default function ExternalItemPage() {
  const { t } = useT();
  const loc = useLocation();
  const qc = useQueryClient();
  const [gone, setGone] = useState(false);
  const id = loc.pathname.replace(/^\/x\//, "");
  const cached = qc.getQueryData<{ items: ExternalShared[]; email_proved: boolean }>(queryKey("shared-external"));
  const item = cached?.items.find((i) => i.id === id) ?? null;

  if (!ITEM_ID.test(id) || gone) {
    return <EmptyState text={t("external.gone")} />;
  }

  const byline = item ? (
    <p style={{ margin: "0 0 16px", color: "var(--ant-color-text-secondary)" }}>
      <span>{t("external.sharedBy")}</span>{" "}
      <bdi>{t("shared.external.by", { name: item.shared_by_name, account: item.account_name })}</bdi>
    </p>
  ) : undefined;

  return <PublicBrowse token="" root={id} source={sessionSource()} byline={byline} onlost={() => setGone(true)} />;
}