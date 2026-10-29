<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tr } from '../lib/i18n';
  import { passwordMinLength } from '../lib/brand';
  import PasswordField from './PasswordField.svelte';

  interface Props {
    idPrefix: string;
    password: string;
    confirm: string;
    labels: { password: string; confirm: string };
    passwordError?: string | null;
    confirmError?: string | null;
  }

  let {
    idPrefix,
    password = $bindable(''),
    confirm = $bindable(''),
    labels,
    passwordError = null,
    confirmError = null,
  }: Props = $props();

  let lengthMet = $derived([...password].length >= $passwordMinLength);
  let matchMet = $derived(password.length > 0 && password === confirm);
</script>

<PasswordField
  id="{idPrefix}-password"
  label={labels.password}
  bind:value={password}
  autocomplete="new-password"
  error={passwordError}
/>
<PasswordField
  id="{idPrefix}-confirm"
  label={labels.confirm}
  bind:value={confirm}
  autocomplete="new-password"
  error={confirmError}
/>

<section class="rules" aria-label={$tr('newPassword.rules')}>
  <p class="rules-title">{$tr('newPassword.rules')}</p>
  <ul>
    <li data-met={lengthMet}>
      {$tr('newPassword.rule.length', { min: $passwordMinLength })}:
      <span class="rule-status">{lengthMet ? $tr('newPassword.status.met') : $tr('newPassword.status.todo')}</span>
    </li>
    <li data-met={matchMet}>
      {$tr('newPassword.rule.match')}:
      <span class="rule-status">{matchMet ? $tr('newPassword.status.met') : $tr('newPassword.status.todo')}</span>
    </li>
  </ul>
</section>
