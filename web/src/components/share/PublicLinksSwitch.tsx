// SPDX-License-Identifier: AGPL-3.0-or-later

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Checkbox } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { getSharingPolicy, setSharingPolicy, type SharingPolicy } from "../../lib/sharing/api";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import DeletePublicLinks from "./DeletePublicLinks";

// PolicyPatch is the part of the account's sharing policy one change may name: the anyone-links switch, the
// external-sharing switch, or both. Each named switch is applied on its own by the server.
type PolicyPatch = { public_links?: boolean; external_sharing?: boolean };

// PublicLinksSwitch is the account's switches for links that anyone can open and for sharing with people in other
// accounts. Turning anyone links off pauses those links at once; turning it on makes them work again. The owner and the
// installation admin both use it (part v4). A failed refetch keeps the answer already shown.
export default function PublicLinksSwitch({ account }: { account: string }) {
  const { t, tp } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const key = queryKey("sharing-policy", account);

  const policy = useQuery({
    queryKey: key,
    queryFn: () => getSharingPolicy(account),
    refetchOnMount: "always",
  });

  const toggle = useMutation({
    mutationFn: (patch: PolicyPatch) => setSharingPolicy(account, patch),
    // The answer is written only if the person is still the one who asked: a mutation's callbacks run after a clear.
    onMutate: () => ({ changed: sessionChanged() }),
    onSuccess: (reply: SharingPolicy, _patch, ctx) => {
      if (ctx.changed()) return;
      qc.setQueryData(key, reply);
    },
  });

  const shown = policy.data;
  const error = toggle.isError ? failureText(toggle.error, lang) : policy.isError && !shown ? failureText(policy.error, lang) : null;
  const notice = toggle.isSuccess ? t("policy.saved") : null;
  const busy = toggle.isPending;

  const change = (patch: PolicyPatch) => {
    const stale = sessionChanged();
    toggle.mutate(patch, {
      // The reply already holds the new policy, so a made change keeps it; only a refused change reads the server again.
      onSettled: (_reply, err) => {
        if (err && !stale()) void qc.invalidateQueries({ queryKey: key });
      },
    });
  };

  return (
    <section aria-labelledby="policy-public-links-title" style={{ display: "grid", gap: 8 }}>
      <h2 id="policy-public-links-title" style={{ margin: "0 0 8px", fontSize: 18, fontWeight: 600 }}>
        {t("policy.title")}
      </h2>
      {error && <Alert type="error" showIcon role="alert" message={error} />}
      {notice && (
        <p className="hint" role="status" style={{ margin: 0, fontSize: 13 }}>
          {notice}
        </p>
      )}
      {shown && (
        <>
          <Checkbox id="public-links-switch" checked={shown.public_links} disabled={busy} onChange={(e) => change({ public_links: e.target.checked })}>
            {t("policy.publicLinks")}
          </Checkbox>
          <p className="hint" style={{ margin: 0, fontSize: 13 }}>
            {t("policy.publicLinksHint")}
          </p>
          <p className="hint" style={{ margin: 0, fontSize: 13 }}>
            {tp("policy.anyoneLinks", shown.anyone_links)}
          </p>
          <DeletePublicLinks account={account} count={shown.anyone_links} ondone={() => void qc.invalidateQueries({ queryKey: key })} onunsure={() => void qc.invalidateQueries({ queryKey: key })} />
          <Checkbox id="external-sharing-switch" checked={shown.external_sharing === true} disabled={busy} onChange={(e) => change({ external_sharing: e.target.checked })}>
            {t("policy.externalSharing")}
          </Checkbox>
          <p className="hint" style={{ margin: 0, fontSize: 13 }}>
            {t("policy.externalSharingHint")}
          </p>
        </>
      )}
    </section>
  );
}
