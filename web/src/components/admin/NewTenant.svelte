<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../../lib/i18n';
  import { createAccount, type NewAccount } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import { limitBytes, type LimitUnit } from '../../lib/quota';
  import type { Account, PersonReply } from '../../lib/manage/types';
  import Dialog from '../Dialog.svelte';

  // NewTenant creates a tenant with its first owner. The owner gets the invitation by email, or a link the page
  // shows once. The storage limit is in decimal units, as the meter shows it; "No limit" sets no quota.
  let {
    onclose,
    oncreated,
  }: {
    onclose: () => void;
    oncreated: (account: Account, owner: PersonReply | undefined) => void;
  } = $props();

  let name = $state('');
  let noLimit = $state(false);
  let amount = $state('');
  let unit = $state<LimitUnit>('GB');
  let email = $state('');
  let ownerName = $state('');
  let how = $state<'email' | 'link'>('email');
  let externalId = $state('');
  let busy = $state(false);
  let error = $state<string | null>(null);

  let emailOk = $derived(/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim()));
  let nameOk = $derived(name.trim() !== '');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy || !nameOk || !emailOk) return;
    let quota = 0;
    if (!noLimit) {
      const bytes = limitBytes(amount, unit);
      if (bytes === null) {
        error = translate($lang, 'tenant.new.limitInvalid');
        return;
      }
      quota = bytes;
    }
    const body: NewAccount = {
      name: name.trim(),
      quota_bytes: quota,
      owner: { email: email.trim(), display_name: ownerName.trim(), verification: how },
    };
    if (externalId.trim() !== '') body.external_id = externalId.trim();
    busy = true;
    error = null;
    try {
      const created = await createAccount(body);
      oncreated(created, created.owner);
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={translate($lang, 'tenant.new.title')} id="new-tenant" wide {onclose}>
  <form class="form" onsubmit={submit} novalidate>
    <div class="field">
      <label for="new-tenant-name">{translate($lang, 'tenant.new.name')}</label>
      <input id="new-tenant-name" type="text" autocomplete="off" bind:value={name} maxlength="200" data-autofocus />
    </div>

    <fieldset class="limit">
      <legend>{translate($lang, 'tenant.new.limit')}</legend>
      <div class="limit-row">
        <input
          id="new-tenant-limit"
          type="text"
          inputmode="decimal"
          autocomplete="off"
          bind:value={amount}
          disabled={noLimit}
          aria-label={translate($lang, 'tenant.new.limitAmount')}
        />
        <select bind:value={unit} disabled={noLimit} aria-label={translate($lang, 'tenant.new.limitUnit')}>
          <option value="GB">GB</option>
          <option value="TB">TB</option>
        </select>
      </div>
      <label class="check">
        <input type="checkbox" bind:checked={noLimit} />
        <span>{translate($lang, 'tenant.new.noLimit')}</span>
      </label>
    </fieldset>

    <fieldset class="owner">
      <legend>{translate($lang, 'tenant.new.owner')}</legend>
      <div class="field">
        <label for="new-tenant-owner-email">{translate($lang, 'tenant.new.ownerEmail')}</label>
        <input id="new-tenant-owner-email" type="email" autocomplete="off" inputmode="email" bind:value={email} />
      </div>
      <div class="field">
        <label for="new-tenant-owner-name">{translate($lang, 'tenant.new.ownerName')}</label>
        <input id="new-tenant-owner-name" type="text" autocomplete="off" bind:value={ownerName} />
      </div>
      <fieldset class="how">
        <legend>{translate($lang, 'tenant.new.how')}</legend>
        <label class="check"><input type="radio" name="how" value="email" bind:group={how} /> <span>{translate($lang, 'tenant.new.howEmail')}</span></label>
        <label class="check"><input type="radio" name="how" value="link" bind:group={how} /> <span>{translate($lang, 'tenant.new.howLink')}</span></label>
      </fieldset>
    </fieldset>

    <details class="advanced">
      <summary>{translate($lang, 'tenant.new.advanced')}</summary>
      <div class="field">
        <label for="new-tenant-external">{translate($lang, 'tenant.new.externalId')}</label>
        <input id="new-tenant-external" type="text" autocomplete="off" bind:value={externalId} />
        <p class="hint">{translate($lang, 'tenant.new.externalHint')}</p>
      </div>
    </details>

    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <div class="foot">
      <button type="button" class="btn secondary" onclick={onclose}>{translate($lang, 'common.cancel')}</button>
      <button type="submit" class="btn primary" disabled={busy || !nameOk || !emailOk}>{translate($lang, 'tenant.new.create')}</button>
    </div>
  </form>
</Dialog>

<style>
  .form {
    display: grid;
    gap: var(--space-4);
  }

  .field,
  .owner,
  .limit,
  .how {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    border: 0;
  }

  .field input,
  .limit input,
  .limit select {
    min-height: 44px;
    padding-inline: var(--space-3);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .limit-row {
    display: flex;
    gap: var(--space-2);
  }

  .limit-row input {
    flex: 1 1 auto;
    min-width: 0;
  }

  .check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
  }

  .hint {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }
</style>
