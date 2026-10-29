<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../lib/i18n';
  import { typedNameMatches } from '../lib/manage/confirm';
  import Dialog from './Dialog.svelte';

  // Confirm asks before a change that cannot be undone or that ends access. With requireName, the person types
  // the name, and the button stays off until the text matches. The parent passes any error text it has.
  let {
    id,
    title,
    message,
    action,
    danger = false,
    requireName = null,
    busy = false,
    error = null,
    onconfirm,
    onclose,
  }: {
    id: string;
    title: string;
    message: string;
    action: string;
    danger?: boolean;
    requireName?: string | null;
    busy?: boolean;
    error?: string | null;
    onconfirm: () => void;
    onclose: () => void;
  } = $props();

  let typed = $state('');
  let ok = $derived(requireName === null || typedNameMatches(typed, requireName));
</script>

<Dialog {title} {id} {onclose}>
  <div class="confirm">
    <p>{message}</p>
    {#if requireName !== null}
      <label class="field-label" for="{id}-name">
        {translate($lang, 'confirm.typeName', { name: requireName })}
      </label>
      <input id="{id}-name" type="text" autocomplete="off" bind:value={typed} />
    {/if}
    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <div class="foot">
      <button type="button" class="btn secondary" onclick={onclose}>{translate($lang, 'common.cancel')}</button>
      <button type="button" class="btn {danger ? 'danger-fill' : 'primary'}" disabled={busy || !ok} onclick={onconfirm}>
        {action}
      </button>
    </div>
  </div>
</Dialog>

<style>
  .confirm {
    display: grid;
    gap: var(--space-3);
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
    margin-block-start: var(--space-2);
  }
</style>
