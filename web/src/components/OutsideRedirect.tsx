// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from "react";
import { goOutside } from "../lib/outsideSignIn";
import { useT } from "../lib/i18n";
import AuthLayout from "./AuthLayout";

// OutsideRedirect leaves the app for the installation's outside sign-in page (decision 185). The move is made once:
// StrictMode runs effects twice, and a second assign would count as a second hop in the loop guard. While the guard
// refuses a third hop in a minute, goOutside shows the signed-out page instead. The page itself never redirects again.
export default function OutsideRedirect({ url }: { url: string }) {
  const { t } = useT();
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    goOutside(url);
  }, [url]);
  return (
    <AuthLayout>
      <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>
        {t("signin.working")}
      </p>
    </AuthLayout>
  );
}