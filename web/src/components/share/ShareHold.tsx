// SPDX-License-Identifier: AGPL-3.0-or-later

// A share opened while signed out (decision 82). The page takes the share's files from the worker at once, into its own
// memory, and only then goes to sign in. The worker may be stopped while the person is signing in, and the share may expire, so the
// files are taken first. They are never written to disk, and they stay in this tab's memory until the share page takes them after
// the sign-in, or until the person cancels, uploads, or signs out, or until five minutes pass with no take (decision 82)
// M1): a person who walks away at the sign-in page does not leave the files in the tab for good.

import { useEffect } from "react";
import { Navigate } from "react-router-dom";
import { takeShare } from "../../lib/pwa/share";

export default function ShareHold({ id, to }: { id: string; to: string }) {
  useEffect(() => {
    void takeShare(id, { signedOut: true });
  }, [id]);
  return <Navigate to={to} replace />;
}
