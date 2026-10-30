<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { untrack } from 'svelte';
  import { lang, translate } from '../../lib/i18n';
  import { failureText } from '../../lib/manage/failure';
  import { limitTextFor, parseAccountLimit, parseUserLimit, type LimitText } from '../../lib/storage/logic';
  import type { LimitUnit } from '../../lib/quota';

  // LimitEditor sets a storage limit by its amount and unit. A person's limit (allowDefault) may follow the account
  // default, which is stored as null; the default itself (allowDefault false) has no default to follow. Empty is
  // the default for a person, and no limit for the default. "0" is no limit in both.
  let {
    current,
    allowDefault,
    onsave,
    oncancel,
  }: {
    current: number | null;
    allowDefault: boolean;
    onsave: (value: number | null) => Promise<void>;
    oncancel: () => void;
  } = $props();

  // The text fields start from the stored limit. An empty field means the default (a person) or no limit (the default).
  const start = untrack(() => limitTextFor(current));
  let amount = $state(start.amount);
  let unit = $state<LimitUnit>(start.unit);
  let error = $state<string | null>(null);
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    const text: LimitText = { amount, unit };
    const parsed = allowDefault ? parseUserLimit(text) : parseAccountLimit(text);
    if (!parsed.ok) {
      error = translate($lang, 'storage.limitInvalid');
      return;
    }
    busy = true;
    error = null;
    try {
      await onsave(parsed.value);
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busy = false;
    }
  }
</script>

<form class="limit-form" onsubmit={submit} novalidate>
  <div class="limit-row">
    <input
      type="text"
      inputmode="decimal"
      autocomplete="off"
      bind:value={amount}
      aria-label={translate($lang, 'storage.limitAmount')}
      aria-describedby="limit-hint"
    />
    <select bind:value={unit} aria-label={translate($lang, 'storage.limitUnit')}>
      <option value="GB">GB</option>
      <option value="TB">TB</option>
    </select>
  </div>
  <p class="hint" id="limit-hint">
    {allowDefault ? translate($lang, 'storage.limitHintPerson') : translate($lang, 'storage.limitHintDefault')}
  </p>
  {#if error}
    <p class="alert error" role="alert">{error}</p>
  {/if}
  <div class="foot">
    <button type="button" class="btn secondary" onclick={oncancel}>{translate($lang, 'common.cancel')}</button>
    <button type="submit" class="btn primary" disabled={busy}>{translate($lang, 'storage.saveLimit')}</button>
  </div>
</form>

<style>
  .limit-form {
    display: grid;
    gap: var(--space-3);
  }

  .limit-row {
    display: flex;
    gap: var(--space-2);
  }

  .limit-row input {
    flex: 1 1 auto;
    min-width: 0;
  }

  .hint {
    margin: 0;
    color: var(--color-muted-foreground);
  }

  .foot {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }
</style>
