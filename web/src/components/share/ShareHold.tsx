// SPDX-License-Identifier: AGPL-3.0-or-later

// A share opened while signed out (decision 82). The page takes the share's files from the worker at once, into its own
// memory, and only then goes to sign in. The worker may be stopped while the person is signing in, and the share may expire, so the
// files are taken first. They are never written to disk, and they stay in this tab's memory until the share page takes them after
// the sign-in, or until the person cancels, uploads, or signs out, or until five minutes pass with no take (decision 82):
// a person who walks away at the sign-in page does not leave the files in the tab for good.

import { useEffect, useRef } from "react";
import { Navigate } from "react-router-dom";
import { Spin } from "antd";
import { takeShare } from "../../lib/pwa/share";
import { outsideSignIn, sendToSignIn } from "../../lib/outsideSignIn";
import { signInURL } from "../../lib/router";

export default function ShareHold({ id, from }: { id: string; from: string }) {
  // The installation's outside sign-in page (decision 185): the move leaves the app, so the take is given its answer
  // first, and the move is made once (StrictMode runs effects twice, and a second assign would count as a second hop).
  const outside = outsideSignIn(from);
  const started = useRef(false);
  useEffect(() => {
    if (outside === null) {
      void takeShare(id, { signedOut: true });
      return;
    }
    if (started.current) return;
    started.current = true;
    void takeShare(id, { signedOut: true }).then(() => sendToSignIn(from));
  }, [id, from, outside]);
  if (outside !== null) {
    return (
      <div role="status" style={{ display: "grid", placeItems: "center", minHeight: "100vh" }}>
        <Spin size="large" />
      </div>
    );
  }
  return <Navigate to={signInURL(from)} replace />;
}