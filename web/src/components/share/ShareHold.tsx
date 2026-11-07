// SPDX-License-Identifier: AGPL-3.0-or-later

// A share opened while signed out (decision 82, round 2). The page takes the share's files from the worker at once, into its own
// memory, and only then goes to sign in. The worker may be stopped while the person is signing in, and the share may expire, so the
// files are taken first. They are never written to disk, and they stay in this tab's memory until the share page takes them after
// the sign-in, or until the person cancels, uploads, or signs out.

import { useEffect } from "react";
import { Navigate } from "react-router-dom";
import { takeShare } from "../../lib/pwa/share";

export default function ShareHold({ id, to }: { id: string; to: string }) {
  useEffect(() => {
    void takeShare(id);
  }, [id]);
  return <Navigate to={to} replace />;
}
