// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button } from "antd";
import { outsideSignIn } from "../lib/outsideSignIn";
import { go } from "../lib/router";
import { useT } from "../lib/i18n";
import AuthLayout from "../components/AuthLayout";

// SignedOutPage is where a sign-out lands when the installation names an outside sign-in page but no signed-out URL,
// and where the loop guard leaves a person whose sign-in keeps coming back here (decision 204). It never redirects by
// itself: only the button moves, to the outside sign-in page.
export default function SignedOutPage() {
  const { t } = useT();
  const url = outsideSignIn("/");
  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("signedOut.title")}</h1>
      <p>{t("signedOut.body")}</p>
      <Button
        type="primary"
        block
        onClick={() => {
          if (url !== null) window.location.assign(url);
          else go("/login", { replace: true });
        }}
      >
        {t("signedOut.signIn")}
      </Button>
    </AuthLayout>
  );
}