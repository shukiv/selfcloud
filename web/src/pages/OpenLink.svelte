<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { lang, translate, tr } from '../lib/i18n';
  import { session } from '../lib/session';
  import { go, signInURL } from '../lib/router';
  import { signOut } from '../lib/auth';
  import { openShareLink, renewShareLink } from '../lib/sharing/api';
  import { forgetSecret, peekSecret, secretFromAddress, stashSecret } from '../lib/sharing/links';
  import { outcomeOf, renewDelay, visitStep, type VisitStep } from '../lib/sharing/anyone';
  import PublicBrowse from '../components/share/PublicBrowse.svelte';
  import DropPage from '../components/share/DropPage.svelte';

  // OpenLink opens the address /s#secret. The secret is read from the fragment, or from this tab's session
  // storage when a sign-in sent the visitor here. The server decides what the link is:
  // - an organisation link, opened by a signed-in member, names its item, and the page goes there;
  // - an anyone link answers a visitor credential, which is kept in this page's memory only, never in storage,
  //   the address or a cookie. The fragment stays in the address so a reload works, and the credential is renewed
  //   before it ends. A password is asked for when the link has one.
  // A dead link gets the one message, and the fragment is dropped from the address. A signed-out visitor who hits
  // one is sent to sign in, with the secret kept in session storage, as the organisation link needs. An
  // organisation link leaves the address for its item, so its secret leaves the address too.
  // uploadOnly is set for a drop link: the page then shows the drop page, which never lists the folder.
  type Bearer = { token: string; expiresAt: string; root: string; uploadOnly: boolean };

  let step = $state<VisitStep | 'working'>('working');
  let password = $state('');
  let bearer = $state<Bearer | null>(null);
  // secret is kept in memory for the length of the visit, so an ended credential can be opened again.
  let secret = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  // renewing is the renewal in flight, if any, so the timer and a refused upload do not renew twice.
  let renewing: Promise<boolean> | null = null;

  onMount(() => {
    const fromAddress = secretFromAddress(window.location.hash);
    if (fromAddress !== '') {
      // The address names the link now; a secret left in storage by an older visit is dropped.
      forgetSecret(window.sessionStorage);
      secret = fromAddress;
    } else {
      secret = peekSecret(window.sessionStorage);
    }
    if (secret === '') {
      step = 'dead';
      stripAddress();
      return;
    }
    void attempt('');
  });

  onDestroy(() => clearTimeout(timer));

  // attempt opens the link once, with a password when one is given, and shows where the answer leads.
  async function attempt(pw: string): Promise<void> {
    step = 'working';
    try {
      const res = await openShareLink(secret, pw === '' ? undefined : pw);
      forgetSecret(window.sessionStorage);
      if ('node_id' in res) {
        go(`/n/${res.node_id}`, { replace: true });
        return;
      }
      bearer = {
        token: res.token,
        expiresAt: res.expires_at,
        root: res.node ?? '',
        uploadOnly: res.upload_only === true,
      };
      step = 'bearer';
      schedule();
    } catch (err) {
      const outcome = outcomeOf(err);
      if (outcome === 'not_found' && $session === null) {
        toSignIn();
        return;
      }
      forgetSecret(window.sessionStorage);
      step = visitStep(outcome, $session !== null);
      // A dead link's secret is no use in the address. A password step, a lock or a failure keeps it, so a
      // reload tries again.
      if (step === 'dead') stripAddress();
    }
  }

  // stripAddress drops the fragment, and the secret in it, from the address and this history entry.
  function stripAddress(): void {
    window.history.replaceState(null, '', window.location.pathname + window.location.search);
  }

  // toSignIn keeps the secret for the sign-in and goes to it. The address loses its fragment, and the page
  // comes back to /s to open the link again.
  function toSignIn(): void {
    stashSecret(secret, window.sessionStorage);
    stripAddress();
    go(signInURL('/s'), { replace: true });
  }

  // switchAccount signs the visitor out and sends it to sign in, for an organisation link opened from the wrong
  // account. The secret waits for the sign-in.
  async function switchAccount(): Promise<void> {
    stashSecret(secret, window.sessionStorage);
    try {
      await signOut();
    } catch {
      // The session is cleared either way; the sign-in page is shown below.
    }
    stripAddress();
    go(signInURL('/s'), { replace: true });
  }

  function schedule(): void {
    clearTimeout(timer);
    if (bearer === null) return;
    timer = setTimeout(() => void renewNow(), renewDelay(bearer.expiresAt, Date.now()));
  }

  // renewNow swaps the credential for a new one. When that is refused, the link is opened again from the secret,
  // which may ask for the password. It answers whether a credential is held afterwards. Calls made while one is in
  // flight share it.
  function renewNow(): Promise<boolean> {
    if (renewing === null) {
      renewing = doRenew().finally(() => {
        renewing = null;
      });
    }
    return renewing;
  }

  async function doRenew(): Promise<boolean> {
    if (bearer === null) return false;
    const old = bearer.token;
    try {
      const res = await renewShareLink(old);
      if (bearer === null || bearer.token !== old) return bearer !== null;
      bearer = { ...bearer, token: res.token, expiresAt: res.expires_at };
      schedule();
      return true;
    } catch {
      bearer = null;
      await attempt('');
      return bearer !== null;
    }
  }

  // onlost is called by the folder view when a call with the credential is refused.
  function onlost(why: 'expired' | 'gone'): void {
    if (why === 'expired') {
      void renewNow();
      return;
    }
    bearer = null;
    clearTimeout(timer);
    if ($session === null) {
      toSignIn();
      return;
    }
    step = 'dead';
  }

  async function submitPassword(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const pw = password;
    password = '';
    await attempt(pw);
  }
</script>

{#if step !== 'bearer'}
  <h1 class="page-title">{$tr('openlink.title')}</h1>
{/if}

{#if step === 'working'}
  <p class="muted" role="status">{translate($lang, 'openlink.working')}</p>
{:else if step === 'password' || step === 'password-wrong'}
  <form class="stack" onsubmit={submitPassword}>
    <p>{translate($lang, 'share.open.passwordAsk')}</p>
    {#if step === 'password-wrong'}
      <p class="alert error" role="alert">{translate($lang, 'share.open.wrong')}</p>
    {/if}
    <div class="field">
      <label for="share-password">{translate($lang, 'share.open.password')}</label>
      <input id="share-password" type="password" autocomplete="off" required bind:value={password} />
    </div>
    <div class="foot">
      <button type="submit" class="btn primary">{translate($lang, 'share.open.submit')}</button>
    </div>
  </form>
{:else if step === 'bearer' && bearer !== null && bearer.uploadOnly}
  <DropPage
    bearer={() => bearer?.token ?? null}
    expiresAt={() => bearer?.expiresAt ?? ''}
    onrenew={renewNow}
    {onlost}
  />
{:else if step === 'bearer' && bearer !== null}
  <PublicBrowse token={bearer.token} root={bearer.root} {onlost} />
{:else if step === 'limited'}
  <p class="alert error" role="alert">{translate($lang, 'share.open.limited')}</p>
{:else if step === 'failed'}
  <p class="alert error" role="alert">{translate($lang, 'error.generic')}</p>
{:else if step === 'dead'}
  <p class="alert error" role="alert">{translate($lang, 'share.open.dead')}</p>
  {#if $session !== null}
    <p class="foot">
      <button type="button" class="btn secondary" onclick={switchAccount}>{translate($lang, 'share.open.otherAccount')}</button>
    </p>
  {/if}
{/if}

<style>
  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }
</style>
