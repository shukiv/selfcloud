<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../../lib/i18n';
  import { createKey } from '../../lib/manage/api';
  import { expiryFor, type ExpiryChoice } from '../../lib/manage/admin';
  import { failureText } from '../../lib/manage/failure';
  import type { NewKey } from '../../lib/manage/types';
  import Dialog from '../Dialog.svelte';

  // NewKey makes an API key for a tenant. The key is handed up once, to be shown in a dialog that holds it only
  // while open.
  let {
    account,
    onclose,
    oncreated,
  }: {
    account: string;
    onclose: () => void;
    oncreated: (key: NewKey) => void;
  } = $props();

  let name = $state('');
  let expires = $state<ExpiryChoice>('never');
  let busy = $state(false);
  let error = $state<string | null>(null);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy || name.trim() === '') return;
    busy = true;
    error = null;
    try {
      const key = await createKey(account, { name: name.trim(), expires_at: expiryFor(expires, new Date()) });
      oncreated(key);
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={translate($lang, 'keys.new.title')} id="new-key" {onclose}>
  <form class="form" onsubmit={submit} novalidate>
    <div class="field">
      <label for="new-key-name">{translate($lang, 'keys.new.name')}</label>
      <input id="new-key-name" type="text" autocomplete="off" bind:value={name} maxlength="200" data-autofocus />
    </div>
    <div class="field">
      <label for="new-key-expires">{translate($lang, 'keys.new.expires')}</label>
      <select id="new-key-expires" bind:value={expires}>
        <option value="never">{translate($lang, 'keys.expires.never')}</option>
        <option value="30">{translate($lang, 'keys.expires.30')}</option>
        <option value="90">{translate($lang, 'keys.expires.90')}</option>
        <option value="365">{translate($lang, 'keys.expires.365')}</option>
      </select>
    </div>
    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <div class="foot">
      <button type="button" class="btn secondary" onclick={onclose}>{translate($lang, 'common.cancel')}</button>
      <button type="submit" class="btn primary" disabled={busy || name.trim() === ''}>{translate($lang, 'keys.new.create')}</button>
    </div>
  </form>
</Dialog>

<style>
  .form {
    display: grid;
    gap: var(--space-4);
  }

  .field {
    display: grid;
    gap: var(--space-1);
  }

  .field input,
  .field select {
    min-height: 44px;
    padding-inline: var(--space-3);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }
</style>
