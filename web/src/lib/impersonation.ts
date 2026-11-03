// SPDX-License-Identifier: AGPL-3.0-or-later

// The sign-in as a tenant user, from the admin's side and the user's. An admin starts it with a sign-in as call, which the
// page answers with a new session; the admin's own session is kept on the server, and the one-hour limit ends the user's
// session. Going back (or the limit) restores the admin's session from the same server record.
import { api, onSignInAsEnded } from './api';
import { clearSession, flash, session, setMe, setSignedIn } from './session';
import { get } from './store';
import { go, signInURL } from './router';
import { homeFor } from './routes';
import type { Me, SignedIn } from './types';

let ending: Promise<void> | null = null;

// adoptSession takes the session a sign-in as answered: the new token is stored, then the person's /me is read, so
// the page knows whose session this is and whether it is an impersonation.
export async function adoptSession(signed: SignedIn): Promise<Me> {
  setSignedIn(signed);
  const me = await api<Me>('GET', '/api/v1/me', { noRedirect: true });
  setMe(me);
  return me;
}

// endImpersonation ends the sign-in as and restores the admin's session. A call made while one is under way shares it, so
// several requests that find the session ended send one end call. When the admin's session cannot be restored, the browser
// goes to the sign-in page. expired is true when the one-hour limit ended the session: the admin is told so.
export function endImpersonation(expired = false): Promise<void> {
  if (ending) return ending;
  ending = (async () => {
    try {
      const back = await api<SignedIn>('POST', '/api/v1/auth/impersonation/end', { noRedirect: true });
      const me = await adoptSession(back);
      if (expired) flash.set('impersonation.expired');
      go(homeFor(me.user), { replace: true });
    } catch {
      clearSession();
      go(signInURL(), { replace: true });
    } finally {
      ending = null;
    }
  })();
  return ending;
}

// A late answer can report the end after the admin's session is back (it left before the restore): the page is no longer
// signed in as anyone else, so there is nothing to end, and an end call would answer 409 and sign the admin out.
onSignInAsEnded((expired) => {
  if (!get(session)?.impersonation) return;
  void endImpersonation(expired);
});
