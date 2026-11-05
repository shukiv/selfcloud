// SPDX-License-Identifier: AGPL-3.0-or-later

import { signInAsUser } from "../../lib/manage/api";
import { personLabel } from "../../lib/manage/people";
import type { Person } from "../../lib/manage/types";
import { useT } from "../../lib/i18n";
import { adoptSession } from "../../lib/impersonation";
import { go } from "../../lib/router";
import { homeFor } from "../../lib/routes";
import { sessionChanged } from "../../lib/session";
import AdminPassword from "./AdminPassword";

// SignInAs starts the admin's sign-in as a tenant user. The admin types its own password; the answer is the user's session,
// which lasts an hour. The password prompt is AdminPassword, the one every admin change that needs the password uses.
export default function SignInAs({ accountID, person, onClose }: { accountID: string; person: Person; onClose: () => void }) {
  const { t } = useT();
  const name = personLabel(person);

  const signIn = async (adminPassword: string) => {
    const changed = sessionChanged();
    const signed = await signInAsUser(accountID, person.id, adminPassword);
    if (changed()) return;
    const me = await adoptSession(signed);
    go(homeFor(me.user), { replace: true });
  };

  return (
    <AdminPassword
      id="signin-as-admin"
      title={t("tenant.signInAs.title", { name })}
      what={t("tenant.signInAs.what", { name })}
      submitLabel={t("tenant.signInAs.submit", { name })}
      onConfirm={signIn}
      onClose={onClose}
    />
  );
}
