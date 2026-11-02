// SPDX-License-Identifier: AGPL-3.0-or-later

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Checkbox } from "antd";
import { useCT } from "../../lib/comments/i18n";
import { useLang } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { getSharingPolicy, setSharingSwitches, type SharingSwitches as Change } from "../../lib/sharing/api";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";

// SharingSwitches is the owner's two switches for comments and for who-viewed (decisions 39 and 51), on the owner's page
// beside the switch for links. Each one is saved on its own. A failed change keeps the switch where the server left it.
// The who-viewed hint says what the list is and is not, and that turning it on again starts a new period.
export default function SharingSwitches({ account }: { account: string }) {
  const { t, ready } = useCT();
  const lang = useLang();
  const qc = useQueryClient();
  const key = queryKey("sharing-policy", account);

  const policy = useQuery({
    queryKey: key,
    queryFn: () => getSharingPolicy(account),
    refetchOnMount: "always",
  });

  const change = useMutation({
    mutationFn: (next: Change) => setSharingSwitches(account, next),
    onMutate: () => ({ changed: sessionChanged() }),
    onSuccess: (reply, _next, ctx) => {
      if (ctx.changed()) return;
      qc.setQueryData(key, reply);
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: key }),
  });

  if (!ready) return null;
  const shown = policy.data;
  const busy = change.isPending;

  return (
    <section className="comments-switches" aria-labelledby="settings-comments-title" style={{ display: "grid", gap: 8, marginBlockStart: 24 }}>
      <h2 id="settings-comments-title" style={{ margin: "0 0 8px", fontSize: 18, fontWeight: 600 }}>
        {t("settings.commentsTitle")}
      </h2>
      {change.isError && <Alert type="error" showIcon role="alert" message={t("settings.failed")} />}
      {policy.isError && !shown && <Alert type="error" showIcon role="alert" message={failureText(policy.error, lang)} />}
      {shown && (
        <>
          <Checkbox
            id="comments-switch"
            checked={shown.comments === true}
            disabled={busy}
            onChange={(e) => change.mutate({ comments: e.target.checked })}
          >
            {t("settings.comments")}
          </Checkbox>
          <p className="hint" style={{ margin: 0, fontSize: 13 }}>
            {t("settings.commentsHint")}
          </p>
          <Checkbox
            id="who-viewed-switch"
            checked={shown.who_viewed === true}
            disabled={busy}
            onChange={(e) => change.mutate({ who_viewed: e.target.checked })}
          >
            {t("settings.whoViewed")}
          </Checkbox>
          <p className="hint" style={{ margin: 0, fontSize: 13 }}>
            {t("settings.whoViewedHint")}
          </p>
        </>
      )}
    </section>
  );
}
