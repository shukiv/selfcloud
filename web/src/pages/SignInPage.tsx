// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { establish, signInWithLink } from "../lib/auth";
import { errorMessage, isLinkFailure, type Message } from "../lib/errors";
import { go } from "../lib/router";
import { signInTarget, looksLikeToken, switchPromptOf, tokenFromFragment, type SwitchPrompt as SwitchQuestion } from "../lib/signin";
import { useT } from "../lib/i18n";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import SwitchPrompt from "../components/SwitchPrompt";

// SignInPage redeems a sign-in link: the token is in the address's fragment, read and removed from
// the address bar before any request. A browser already signed in as someone else is asked first.
export default function SignInPage() {
  const { t } = useT();
  const [error, setError] = useState<Message | null>(null);
  const [working, setWorking] = useState(true);
  const [prompt, setPrompt] = useState<SwitchQuestion | null>(null);
  // The token is kept only for a switch the person confirms.
  const [token, setToken] = useState("");
  const started = useRef(false);

  async function redeem(value: string, confirm: boolean) {
    if (!looksLikeToken(value)) {
      setError({ key: "signin.failed" });
      setWorking(false);
      return;
    }
    setPrompt(null);
    setWorking(true);
    try {
      const signed = await signInWithLink(value, confirm);
      await establish(signed);
      go(signInTarget(signed.next, signed.user), { replace: true });
    } catch (err) {
      const asked = switchPromptOf(err);
      if (asked) {
        setPrompt(asked);
      } else {
        setError(isLinkFailure(err) ? { key: "signin.failed" } : errorMessage(err, "link"));
      }
    } finally {
      setWorking(false);
    }
  }

  useEffect(() => {
    // StrictMode runs effects twice; a link is good for one redemption, so only the first run counts.
    if (started.current) return;
    started.current = true;
    const value = tokenFromFragment(window.location.hash);
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    setToken(value);
    void redeem(value, false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // stay keeps the browser as it is and does not use the link.
  function stay() {
    setPrompt(null);
    go("/files", { replace: true });
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("signin.title")}</h1>
      {working && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>
          {t("signin.working")}
        </p>
      )}
      <Alert message={error} />
      <SwitchPrompt prompt={prompt} onConfirm={() => void redeem(token, true)} onStay={stay} />
    </AuthLayout>
  );
}
