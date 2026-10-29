<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { onMount, tick } from 'svelte';

  // Dialog is a modal: it holds focus inside while open, closes on Escape, and gives focus back to what
  // opened it when it closes.
  interface Props {
    title: string;
    id: string;
    onclose: () => void;
    children: Snippet;
    wide?: boolean;
  }

  let { title, id, onclose, children, wide = false }: Props = $props();
  let panel: HTMLDivElement | undefined = $state();
  const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])';

  onMount(() => {
    const opener = document.activeElement as HTMLElement | null;
    void tick().then(() => {
      const first = panel?.querySelector<HTMLElement>('[data-autofocus]') ?? panel?.querySelector<HTMLElement>(FOCUSABLE);
      first?.focus();
    });
    return () => opener?.focus?.();
  });

  function keydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      onclose();
      return;
    }
    if (e.key !== 'Tab' || !panel) return;
    const items = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.offsetParent !== null);
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }
</script>

<div class="scrim" role="presentation" onkeydown={keydown}>
  <div class="dialog" class:wide role="dialog" aria-modal="true" aria-labelledby="{id}-title" bind:this={panel}>
    <h2 class="dialog-title" id="{id}-title">{title}</h2>
    {@render children()}
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--space-4);
    background: rgb(15 23 42 / 0.45);
  }

  .dialog {
    width: 100%;
    max-width: 420px;
    max-height: calc(100dvh - 2 * var(--space-4));
    overflow: auto;
    padding: var(--space-6);
    background: var(--color-surface);
    color: var(--color-foreground);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-pop);
  }

  .dialog.wide {
    max-width: 640px;
  }

  .dialog-title {
    margin: 0 0 var(--space-4);
    font-size: var(--text-lg);
    font-weight: 600;
  }
</style>
