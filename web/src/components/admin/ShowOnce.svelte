<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../../lib/i18n';
  import Dialog from '../Dialog.svelte';

  // ShowOnce shows a secret the server gives once: an API key, or a link to pass on. The value lives only in the
  // parent's state while this dialog is open. It is not stored, logged or put in the URL. The dialog closes only
  // by its button, so the secret is not dismissed by a stray Escape.
  let {
    id,
    title,
    label,
    value,
    warning,
    onclosed,
  }: {
    id: string;
    title: string;
    label: string;
    value: string;
    warning: string;
    onclosed: () => void;
  } = $props();

  let copied = $state(false);
  let field: HTMLInputElement | undefined = $state();

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      copied = true;
    } catch {
      // The clipboard is not available here; the text is selected, so it can be copied by hand.
      copied = false;
      field?.select();
    }
  }
</script>

<Dialog {title} {id} onclose={() => undefined}>
  <div class="once">
    <p class="alert warn" role="note">{warning}</p>
    <div class="field">
      <label for="{id}-value">{label}</label>
      <div class="copy-row">
        <input id="{id}-value" type="text" readonly value={value} bind:this={field} onfocus={(e) => (e.currentTarget as HTMLInputElement).select()} />
        <button type="button" class="btn secondary" onclick={copy}>{translate($lang, 'secret.copy')}</button>
      </div>
      <p class="live" role="status" aria-live="polite">{copied ? translate($lang, 'secret.copied') : ''}</p>
    </div>
    <div class="foot">
      <button type="button" class="btn primary" onclick={onclosed}>{translate($lang, 'secret.done')}</button>
    </div>
  </div>
</Dialog>

<style>
  .once {
    display: grid;
    gap: var(--space-4);
  }

  .field {
    display: grid;
    gap: var(--space-1);
  }

  .copy-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .copy-row input {
    flex: 1 1 220px;
    min-height: 44px;
    padding-inline: var(--space-3);
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    color: var(--color-foreground);
    background: var(--color-muted);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .alert.warn {
    color: var(--color-warning-fg);
    background: var(--color-warning-bg);
    border-color: transparent;
  }

  .live {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .live:empty {
    display: none;
  }

  .foot {
    display: flex;
    justify-content: flex-end;
  }
</style>
