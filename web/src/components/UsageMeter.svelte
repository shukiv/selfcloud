<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../lib/i18n';
  import { formatBytes } from '../lib/files/format';

  // UsageMeter shows how much of a quota is used. It turns warning at 80% and danger at 95%, and the text says
  // so too. A null quota is no limit: the meter then shows the size with no bar.
  let { used, quota }: { used: number; quota: number | null } = $props();

  let share = $derived(quota && quota > 0 ? Math.min(1, used / quota) : 0);
  let level = $derived(quota && quota > 0 ? (share >= 0.95 ? 'danger' : share >= 0.8 ? 'warning' : 'ok') : 'none');
  let text = $derived(
    quota && quota > 0
      ? translate($lang, 'usage.ofLimit', { used: formatBytes(used, $lang), limit: formatBytes(quota, $lang) })
      : translate($lang, 'usage.noLimit', { used: formatBytes(used, $lang) }),
  );
  let stateText = $derived(
    level === 'danger' ? translate($lang, 'usage.danger') : level === 'warning' ? translate($lang, 'usage.warning') : '',
  );
</script>

<div class="meter {level}">
  <p class="meter-text">
    <span>{text}</span>
    {#if stateText}<span class="meter-state">{stateText}</span>{/if}
  </p>
  {#if quota && quota > 0}
    <div class="bar" role="meter" aria-valuemin="0" aria-valuemax={quota} aria-valuenow={Math.min(used, quota)} aria-label={translate($lang, 'usage.label')}>
      <span class="fill" style:inline-size="{Math.round(share * 100)}%"></span>
    </div>
  {/if}
</div>

<style>
  .meter {
    display: grid;
    gap: var(--space-2);
  }

  .meter-text {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: space-between;
    font-variant-numeric: tabular-nums;
  }

  .meter-state {
    font-weight: 600;
  }

  .bar {
    block-size: 8px;
    background: var(--color-muted);
    border-radius: var(--radius-full);
    overflow: hidden;
  }

  .fill {
    display: block;
    block-size: 100%;
    background: var(--color-primary);
    border-radius: var(--radius-full);
  }

  .warning .fill {
    background: var(--color-accent);
  }

  .danger .fill {
    background: var(--color-danger);
  }

  .warning .meter-state {
    color: var(--color-warning-fg);
  }

  .danger .meter-state {
    color: var(--color-danger);
  }
</style>
