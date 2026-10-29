<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tick } from 'svelte';
  import { tr } from '../lib/i18n';
  import { ApiError } from '../lib/api';
  import { establish, signIn } from '../lib/auth';
  import { errorMessage, type Message } from '../lib/errors';
  import { go } from '../lib/router';
  import { safeNext } from '../lib/next';
  import type { AccountChoice } from '../lib/types';
  import Alert from '../components/Alert.svelte';
  import PasswordField from '../components/PasswordField.svelte';
  import TextField from '../components/TextField.svelte';

  let email = $state('');
  let password = $state('');
  let emailError = $state<string | null>(null);
  let passwordError = $state<string | null>(null);
  let error = $state<Message | null>(null);
  // accounts is set when the email belongs to several accounts; the person then picks one.
  let accounts = $state<AccountChoice[] | null>(null);
  let account = $state('');
  let busy = $state(false);

  async function focusFirstInvalid(id: string) {
    await tick();
    document.getElementById(id)?.focus();
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    error = null;
    emailError = null;
    passwordError = null;
    if (email.trim() === '') {
      emailError = 'error.emailRequired';
      return focusFirstInvalid('login-email');
    }
    if (password === '') {
      passwordError = 'error.passwordRequired';
      return focusFirstInvalid('login-password');
    }
    busy = true;
    try {
      const signed = await signIn(email.trim(), password, account || undefined);
      await establish(signed);
      go(safeNext(new URLSearchParams(window.location.search).get('next')), { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && Array.isArray(err.details.accounts)) {
        accounts = err.details.accounts as AccountChoice[];
        account = accounts[0]?.id ?? '';
        await focusFirstInvalid('login-account-0');
      } else {
        error = errorMessage(err, 'login');
      }
    } finally {
      busy = false;
    }
  }
</script>

<h1 class="page-title">{$tr('login.title')}</h1>

<Alert message={error} />

<form onsubmit={submit} novalidate>
  <TextField
    id="login-email"
    label={$tr('login.email')}
    type="email"
    inputmode="email"
    autocomplete="username"
    bind:value={email}
    error={emailError ? $tr(emailError) : null}
  />
  <PasswordField
    id="login-password"
    label={$tr('login.password')}
    autocomplete="current-password"
    bind:value={password}
    error={passwordError ? $tr(passwordError) : null}
  />
  <p class="forgot"><a href="/forgot">{$tr('login.forgot')}</a></p>

  {#if accounts}
    <fieldset class="choices">
      <legend>{$tr('login.pick.title')}</legend>
      <p class="muted">{$tr('login.pick.body')}</p>
      {#each accounts as choice, i (choice.id)}
        <label class="choice">
          <input id="login-account-{i}" type="radio" name="account" value={choice.id} bind:group={account} />
          <bdi>{choice.name}</bdi>
        </label>
      {/each}
    </fieldset>
  {/if}

  <button type="submit" class="btn primary block" disabled={busy}>
    {accounts ? $tr('login.pick.submit') : $tr('login.submit')}
  </button>
</form>
