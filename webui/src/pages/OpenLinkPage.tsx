// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type FormEvent } from "react";
import { Alert, Button, Input } from "antd";
import { useT } from "../lib/i18n";
import { useStore, get } from "../lib/store";
import { go, signInURL } from "../lib/router";
import { validID } from "../lib/files/route";
import { signOut } from "../lib/auth";
import { session, sessionChanged, sessionKey } from "../lib/session";
import { openShareLink, renewShareLink, type OpenedLink } from "../lib/sharing/api";
import { forgetSecret, peekSecret, secretFromAddress, stashSecret } from "../lib/sharing/links";
import { outcomeOf, renewDelay, visitStep, type VisitStep } from "../lib/sharing/anyone";
import PublicBrowse from "../components/share/PublicBrowse";
import DropPage from "../components/share/DropPage";

// OpenLink opens the address /s#secret. The secret is read from the fragment, or from this tab's session storage when
// a sign-in sent the visitor here. The server decides what the link is:
// - an organisation link, opened by a signed-in member, names its item, and the page goes there;
// - an anyone link answers a visitor credential, which is kept in this page's memory only, never in storage,
//   the address or a cookie. The fragment stays in the address so a reload works, and the credential is renewed
//   before it ends. A password is asked for when the link has one.
// A dead link gets the one message, and the fragment is dropped from the address. A signed-out visitor who hits one
// is sent to sign in, with the secret kept in session storage, as the organisation link needs. An organisation link
// leaves the address for its item, so its secret leaves the address too.
// uploadOnly is set for a drop link: the page then shows the drop page, which never lists the folder.
// The secret, the credential and the timer live in refs, not in the router, so no re-render can open the link again.
type Bearer = { token: string; expiresAt: string; root: string; uploadOnly: boolean };
type Step = VisitStep | "working";

// stripAddress drops the fragment, and the secret in it, from the address and this history entry.
function stripAddress(): void {
  window.history.replaceState(null, "", window.location.pathname + window.location.search);
}

export default function OpenLinkPage() {
  const { t } = useT();
  const me = useStore(session);
  const signedIn = me !== null;
  const key = sessionKey(me);

  const [step, setStep] = useState<Step>("working");
  const [password, setPassword] = useState("");
  const [bearer, setBearerState] = useState<Bearer | null>(null);
  const bearerRef = useRef<Bearer | null>(null);
  // secret is kept in memory for the length of the visit, so an ended credential can be opened again.
  const secretRef = useRef("");
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  // renewing is the renewal in flight, if any, so the timer and a refused upload do not renew twice.
  const renewingRef = useRef<Promise<boolean> | null>(null);
  // started guards the one open a visit makes: StrictMode runs the mount effect twice on the same instance.
  const startedRef = useRef(false);
  const aliveRef = useRef(true);
  const lastKeyRef = useRef<string | null>(null);

  const setBearer = (b: Bearer | null): void => {
    bearerRef.current = b;
    setBearerState(b);
  };

  const signedInNow = (): boolean => get(session) !== null;

  const clearTimer = (): void => clearTimeout(timerRef.current);

  const schedule = (): void => {
    clearTimer();
    const b = bearerRef.current;
    if (b === null || !aliveRef.current) return;
    timerRef.current = setTimeout(() => void renewNow(), renewDelay(b.expiresAt, Date.now()));
  };

  // toSignIn keeps the secret for the sign-in and goes to it. The address loses its fragment, and the page comes back
  // to /s to open the link again.
  const toSignIn = (): void => {
    stashSecret(secretRef.current, window.sessionStorage);
    stripAddress();
    go(signInURL("/s"), { replace: true });
  };

  // attempt opens the link once, with a password when one is given, and shows where the answer leads. An answer that
  // arrives after the session changed, or after the page left, is dropped.
  const attempt = async (pw: string): Promise<void> => {
    setStep("working");
    const changed = sessionChanged();
    let res: OpenedLink;
    try {
      res = await openShareLink(secretRef.current, pw === "" ? undefined : pw);
    } catch (err) {
      if (!aliveRef.current || changed()) {
        forgetSecret(window.sessionStorage);
        return;
      }
      const outcome = outcomeOf(err);
      if (outcome === "not_found" && !signedInNow()) {
        toSignIn();
        return;
      }
      forgetSecret(window.sessionStorage);
      const next = visitStep(outcome, signedInNow());
      setStep(next);
      // A dead link's secret is no use in the address. A password step, a lock or a failure keeps it, so a reload
      // tries again.
      if (next === "dead") stripAddress();
      return;
    }
    forgetSecret(window.sessionStorage);
    if (!aliveRef.current || changed()) return;
    if ("node_id" in res) {
      // An organisation link names its item; the address moves there, and the fragment goes with the old address.
      // A server id that is not in the shape of one is not followed: the page says the link does not work.
      if (!validID("nod", res.node_id)) {
        setStep("dead");
        stripAddress();
        return;
      }
      go(`/n/${res.node_id}`, { replace: true });
      return;
    }
    setBearer({ token: res.token, expiresAt: res.expires_at, root: res.node ?? "", uploadOnly: res.upload_only === true });
    setStep("bearer");
    schedule();
  };

  // renewNow swaps the credential for a new one. When that is refused, the link is opened again from the secret,
  // which may ask for the password. It answers whether a credential is held afterwards. Calls made while one is in
  // flight share it.
  const renewNow = (): Promise<boolean> => {
    if (renewingRef.current === null) {
      renewingRef.current = doRenew().finally(() => {
        renewingRef.current = null;
      });
    }
    return renewingRef.current;
  };

  const doRenew = async (): Promise<boolean> => {
    const old = bearerRef.current;
    if (old === null) return false;
    const changed = sessionChanged();
    try {
      const res = await renewShareLink(old.token);
      const cur = bearerRef.current;
      if (cur === null || cur.token !== old.token) return cur !== null;
      if (changed() || !aliveRef.current) {
        setBearer(null);
        clearTimer();
        return false;
      }
      setBearer({ ...cur, token: res.token, expiresAt: res.expires_at });
      schedule();
      return true;
    } catch {
      setBearer(null);
      clearTimer();
      await attempt("");
      return bearerRef.current !== null;
    }
  };

  // onlost is called by the folder view when a call with the credential is refused.
  const onlost = (why: "expired" | "gone"): void => {
    if (why === "expired") {
      void renewNow();
      return;
    }
    setBearer(null);
    clearTimer();
    if (!signedInNow()) {
      toSignIn();
      return;
    }
    setStep("dead");
  };

  // switchAccount signs the visitor out and sends it to sign in, for an organisation link opened from the wrong
  // account. The secret waits for the sign-in.
  const switchAccount = async (): Promise<void> => {
    stashSecret(secretRef.current, window.sessionStorage);
    try {
      await signOut();
    } catch {
      // The session is cleared either way; the sign-in page is shown below.
    }
    stripAddress();
    go(signInURL("/s"), { replace: true });
  };

  // endVisit drops everything the visit holds when the session changes under it: the credential, the timer, the
  // secret and any typed password. The address loses its secret, and the page shows the one dead-link message.
  const endVisit = (): void => {
    clearTimer();
    setBearer(null);
    secretRef.current = "";
    setPassword("");
    stripAddress();
    setStep("dead");
  };

  // Mount: read the secret once, from the address (then drop any older stored one) or from storage.
  useEffect(() => {
    aliveRef.current = true;
    if (!startedRef.current) {
      startedRef.current = true;
      const fromAddress = secretFromAddress(window.location.hash);
      if (fromAddress !== "") {
        forgetSecret(window.sessionStorage);
        secretRef.current = fromAddress;
      } else {
        secretRef.current = peekSecret(window.sessionStorage);
        // A fragment that is not a secret has no place in the address.
        if (window.location.hash !== "") stripAddress();
      }
      if (secretRef.current === "") {
        setStep("dead");
        stripAddress();
      } else {
        void attempt("");
      }
    }
    return () => {
      aliveRef.current = false;
      clearTimer();
    };
    // attempt and the helpers read refs only, so the first render's closures stay correct.
  }, []);

  // A change of session ends the visit held by this page: no credential from the previous session is kept.
  useEffect(() => {
    if (lastKeyRef.current === null) {
      lastKeyRef.current = key;
      return;
    }
    if (lastKeyRef.current === key) return;
    lastKeyRef.current = key;
    endVisit();
  }, [key]);

  const onSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const pw = password;
    setPassword("");
    await attempt(pw);
  };

  return (
    <div>
      {step !== "bearer" && <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("openlink.title")}</h1>}

      {step === "working" && (
        <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>
          {t("openlink.working")}
        </p>
      )}

      {(step === "password" || step === "password-wrong") && (
        <form onSubmit={(e) => void onSubmit(e)} style={{ display: "grid", gap: 12 }}>
          <p>{t("share.open.passwordAsk")}</p>
          {step === "password-wrong" && <Alert type="error" showIcon role="alert" message={t("share.open.wrong")} />}
          <div style={{ display: "grid", gap: 6 }}>
            <label htmlFor="share-password">{t("share.open.password")}</label>
            <Input.Password
              id="share-password"
              autoComplete="off"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end" }}>
            <Button type="primary" htmlType="submit">
              {t("share.open.submit")}
            </Button>
          </div>
        </form>
      )}

      {step === "bearer" && bearer !== null && bearer.uploadOnly && (
        <DropPage
          bearer={() => bearerRef.current?.token ?? null}
          expiresAt={() => bearerRef.current?.expiresAt ?? ""}
          onrenew={renewNow}
          onlost={onlost}
        />
      )}

      {step === "bearer" && bearer !== null && !bearer.uploadOnly && (
        <PublicBrowse token={bearer.token} root={bearer.root} onlost={onlost} />
      )}

      {step === "limited" && <Alert type="error" showIcon role="alert" message={t("share.open.limited")} />}
      {step === "failed" && <Alert type="error" showIcon role="alert" message={t("error.generic")} />}

      {step === "dead" && (
        <>
          <Alert type="error" showIcon role="alert" message={t("share.open.dead")} />
          {signedIn && (
            <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end", marginTop: 12 }}>
              <Button onClick={() => void switchAccount()}>{t("share.open.otherAccount")}</Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
