<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { get } from 'svelte/store';
  import { tr } from '../lib/i18n';
  import { passwordMinLength } from '../lib/brand';
  import { brand } from '../lib/brand';
  import { establish, verifyAccount } from '../lib/auth';
  import { errorMessage, isLinkFailure, type Message } from '../lib/errors';
  import { go } from '../lib/router';
  import Alert from '../components/Alert.svelte';
  import NewPasswordFields from '../components/NewPasswordFields.svelte';

  // The token sits in the fragment, so it never reaches a server log. It is read once and then
  // removed from the address bar.
  let token = '';
  let linkUsable = $state(true);
  let password = $state('');
  let confirm = $state('');
  let passwordError = $state<string | null>(null);
  let confirmError = $state<string | null>(null);
  let error = $state<Message | null>(null);
  let busy = $state(false);

  onMount(() => {
    token = new URLSearchParams(window.location.hash.slice(1)).get('token') ?? '';
    window.history.replaceState(null, '', window.location.pathname + window.location.search);
    if (token === '') {
      linkUsable = false;
      error = { key: 'error.linkInvalid' };
    }
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    error = null;
    passwordError = null;
    confirmError = null;
    const length = [...password].length;
    if (length < get(passwordMinLength)) {
      passwordError = 'error.passwordShort';
    } else if (length > 256) {
      passwordError = 'error.passwordLong';
    } else if (password !== confirm) {
      confirmError = 'error.passwordMismatch';
    }
    if (passwordError || confirmError) {
      await tick();
      document.getElementById(passwordError ? 'verify-password' : 'verify-confirm')?.focus();
      return;
    }
    busy = true;
    try {
      const signed = await verifyAccount(token, password);
      await establish(signed);
      go('/', { replace: true });
    } catch (err) {
      if (isLinkFailure(err)) linkUsable = false;
      error = errorMessage(err, 'link');
    } finally {
      busy = false;
    }
  }
</script>

<h1 class="page-title">{$tr('verify.title')}</h1>
<p class="muted">{$tr('verify.body', { product: $brand.name })}</p>

<Alert message={error} />

{#if linkUsable}
  <form onsubmit={submit} novalidate>
    <NewPasswordFields
      idPrefix="verify"
      bind:password
      bind:confirm
      labels={{ password: $tr('newPassword.label'), confirm: $tr('newPassword.confirm') }}
      passwordError={passwordError ? $tr(passwordError, { min: $passwordMinLength }) : null}
      confirmError={confirmError ? $tr(confirmError) : null}
    />
    <button type="submit" class="btn primary block" disabled={busy}>{$tr('verify.submit')}</button>
  </form>
{/if}
