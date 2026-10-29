<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tick } from 'svelte';
  import { tr } from '../lib/i18n';
  import { forgotPassword } from '../lib/auth';
  import { errorMessage, type Message } from '../lib/errors';
  import Alert from '../components/Alert.svelte';
  import Chevron from '../components/Chevron.svelte';
  import TextField from '../components/TextField.svelte';

  let email = $state('');
  let emailError = $state<string | null>(null);
  let error = $state<Message | null>(null);
  let sent = $state(false);
  let busy = $state(false);

  // The answer is the same whether or not the address has an account, so the page never says which.
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    error = null;
    emailError = null;
    if (email.trim() === '') {
      emailError = 'error.emailRequired';
      await tick();
      document.getElementById('forgot-email')?.focus();
      return;
    }
    busy = true;
    try {
      await forgotPassword(email.trim());
      sent = true;
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }
</script>

<h1 class="page-title">{$tr('forgot.title')}</h1>
<p class="muted">{$tr('forgot.body')}</p>

<Alert message={error} />
{#if sent}
  <Alert message={{ key: 'forgot.sent' }} tone="success" />
{/if}

<form onsubmit={submit} novalidate>
  <TextField
    id="forgot-email"
    label={$tr('login.email')}
    type="email"
    inputmode="email"
    autocomplete="username"
    bind:value={email}
    error={emailError ? $tr(emailError) : null}
  />
  <button type="submit" class="btn primary block" disabled={busy}>{$tr('forgot.submit')}</button>
</form>
<p class="back-link"><a href="/login"><Chevron />{$tr('forgot.back')}</a></p>
