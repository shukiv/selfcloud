<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from 'svelte';

  // Badge names a state in words, with a colour that says it again. Colour alone is never the signal.
  let { tone = 'neutral', children }: { tone?: 'ok' | 'warn' | 'danger' | 'neutral'; children: Snippet } = $props();
</script>

<span class="badge {tone}">{@render children()}</span>

<style>
  .badge {
    display: inline-block;
    padding: 0 var(--space-2);
    font-size: var(--text-xs);
    font-weight: 500;
    line-height: 20px;
    color: var(--color-muted-foreground);
    background: var(--color-muted);
    border-radius: var(--radius-full);
    white-space: nowrap;
  }

  .ok {
    color: var(--color-success-fg);
    background: var(--color-success-bg);
  }

  .warn {
    color: var(--color-warning-fg);
    background: var(--color-warning-bg);
  }

  .danger {
    color: var(--color-danger);
    background: var(--color-muted);
  }
</style>
