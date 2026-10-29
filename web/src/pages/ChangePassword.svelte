<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tick } from 'svelte';
  import { get } from 'svelte/store';
  import { tr } from '../lib/i18n';
  import { passwordMinLength } from '../lib/brand';
  import { changePassword, loadMe } from '../lib/auth';
  import { errorMessage, type Message } from '../lib/errors';
  import { go } from '../lib/router';
  import { flash } from '../lib/session';
  import Alert from '../components/Alert.svelte';
  import NewPasswordFields from '../components/NewPasswordFields.svelte';
  import PasswordField from '../components/PasswordField.svelte';

  let current = $state('');
  let password = $state('');
  let confirm = $state('');
  let currentError = $state<string | null>(null);
  let passwordError = $state<string | null>(null);
  let confirmError = $state<string | null>(null);
  let error = $state<Message | null>(null);
  let busy = $state(false);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    error = null;
    currentError = null;
    passwordError = null;
    confirmError = null;
    const length = [...password].length;
    if (current === '') {
      currentError = 'error.passwordRequired';
    } else if (length < get(passwordMinLength)) {
      passwordError = 'error.passwordShort';
    } else if (length > 256) {
      passwordError = 'error.passwordLong';
    } else if (password !== confirm) {
      confirmError = 'error.passwordMismatch';
    }
    if (currentError || passwordError || confirmError) {
      await tick();
      const id = currentError ? 'change-current' : passwordError ? 'change-password' : 'change-confirm';
      document.getElementById(id)?.focus();
      return;
    }
    busy = true;
    try {
      await changePassword(current, password);
      // The server replaces this session's cookie, so the token in memory is stale until it is read again.
      await loadMe();
      flash.set('change.done');
      go('/', { replace: true });
    } catch (err) {
      error = errorMessage(err, 'password');
    } finally {
      busy = false;
    }
  }
</script>

<h1 class="page-title">{$tr('change.title')}</h1>

<Alert message={error} />

<form onsubmit={submit} novalidate>
  <PasswordField
    id="change-current"
    label={$tr('change.current')}
    autocomplete="current-password"
    bind:value={current}
    error={currentError ? $tr(currentError) : null}
  />
  <NewPasswordFields
    idPrefix="change"
    bind:password
    bind:confirm
    labels={{ password: $tr('change.new'), confirm: $tr('change.confirm') }}
    passwordError={passwordError ? $tr(passwordError, { min: $passwordMinLength }) : null}
    confirmError={confirmError ? $tr(confirmError) : null}
  />
  <button type="submit" class="btn primary" disabled={busy}>{$tr('change.submit')}</button>
</form>
