<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../../lib/i18n';
  import { failureText } from '../../lib/manage/failure';
  import Dialog from '../Dialog.svelte';

  // NameDialog asks for one name, to create or rename a group or a team space. The server checks it; a refusal
  // is shown by its code. onsave resolves when the name is saved, and the dialog then closes.
  let {
    id,
    title,
    label,
    initial = '',
    action,
    onsave,
    onclose,
  }: {
    id: string;
    title: string;
    label: string;
    initial?: string;
    action: string;
    onsave: (name: string) => Promise<void>;
    onclose: () => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  let name = $state(initial);
  let busy = $state(false);
  let error = $state<string | null>(null);
  let ok = $derived(name.trim() !== '' && name.trim().length <= 255);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!ok || busy) return;
    busy = true;
    error = null;
    try {
      await onsave(name.trim());
    } catch (err) {
      error = failureText(err, $lang);
      busy = false;
    }
  }
</script>

<Dialog {title} {id} {onclose}>
  <form class="form" onsubmit={submit} novalidate>
    <div class="field">
      <label for="{id}-name">{label}</label>
      <input id="{id}-name" type="text" autocomplete="off" bind:value={name} maxlength="255" data-autofocus />
    </div>
    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <div class="foot">
      <button type="button" class="btn secondary" onclick={onclose}>{translate($lang, 'common.cancel')}</button>
      <button type="submit" class="btn primary" disabled={busy || !ok}>{action}</button>
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

  .field input {
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
