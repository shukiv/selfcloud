<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tr } from '../lib/i18n';

  // UndoToast offers to undo the last action for a few seconds. Its text is announced through a live region.
  let { label, expiresAt, onundo, onexpire, status = '' }: {
    label: string;
    expiresAt: number;
    onundo: () => void;
    onexpire: () => void;
    status?: string;
  } = $props();

  // The window closes at expiresAt; the timer runs against the time it was given.
  $effect(() => {
    const end = expiresAt;
    const timer = setInterval(() => {
      if (Date.now() >= end) onexpire();
    }, 250);
    return () => clearInterval(timer);
  });
</script>

<div class="toast" role="group" aria-label={$tr('files.undo.group')}>
  <p class="toast-text" aria-live="polite">{status || label}</p>
  {#if !status}
    <button type="button" class="btn secondary" onclick={onundo}>{$tr('files.undo.action')}</button>
  {/if}
  <button type="button" class="btn secondary" onclick={onexpire} aria-label={$tr('common.dismiss')}>×</button>
</div>

<style>
  .toast {
    position: fixed;
    inset-block-end: var(--space-4);
    inset-inline-start: var(--space-4);
    z-index: 40;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
    max-width: min(560px, calc(100vw - 2 * var(--space-4)));
    padding: var(--space-3) var(--space-4);
    background: var(--color-surface);
    color: var(--color-foreground);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-pop);
  }

  .toast-text {
    margin: 0;
    flex: 1 1 200px;
  }
</style>
