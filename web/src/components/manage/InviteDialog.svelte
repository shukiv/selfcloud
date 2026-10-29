<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate, LANGUAGES, NATIVE_NAMES, type Lang } from '../../lib/i18n';
  import { createPerson, type NewPerson } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import type { PersonReply } from '../../lib/manage/types';
  import Dialog from '../Dialog.svelte';

  // InviteDialog invites a person by email. The invitation is always by email for an owner: a link to pass on is
  // for API keys and installation admins only.
  let {
    account,
    onclose,
    oncreated,
  }: {
    account: string;
    onclose: () => void;
    oncreated: (reply: PersonReply) => void;
  } = $props();

  let email = $state('');
  let name = $state('');
  let role = $state<'member' | 'owner'>('member');
  let locale = $state<Lang>($lang);
  let busy = $state(false);
  let error = $state<string | null>(null);
  let emailOk = $derived(/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim()));

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!emailOk || busy) return;
    busy = true;
    error = null;
    const body: NewPerson = {
      email: email.trim(),
      display_name: name.trim(),
      locale,
      role,
      verification: 'email',
    };
    try {
      const reply = await createPerson(account, body);
      oncreated(reply);
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={translate($lang, 'people.invite.title')} id="invite" wide {onclose}>
  <form class="form" onsubmit={submit} novalidate>
    <div class="field">
      <label for="invite-email">{translate($lang, 'people.invite.email')}</label>
      <input id="invite-email" type="email" autocomplete="off" inputmode="email" bind:value={email} aria-describedby="invite-email-hint" aria-invalid={email !== '' && !emailOk ? 'true' : undefined} data-autofocus />
      <p class="hint" id="invite-email-hint">{translate($lang, 'people.invite.emailHint')}</p>
    </div>
    <div class="field">
      <label for="invite-name">{translate($lang, 'people.invite.name')}</label>
      <input id="invite-name" type="text" autocomplete="off" bind:value={name} />
    </div>
    <fieldset class="choice-set">
      <legend>{translate($lang, 'people.invite.role')}</legend>
      <label class="choice"><input type="radio" name="invite-role" value="member" bind:group={role} /> <span>{translate($lang, 'people.role.member')}</span></label>
      <label class="choice"><input type="radio" name="invite-role" value="owner" bind:group={role} /> <span>{translate($lang, 'people.role.owner')}</span></label>
      {#if role === 'owner'}
        <p class="alert warn" role="note">{translate($lang, 'people.invite.ownerWarning')}</p>
      {/if}
    </fieldset>
    <div class="field">
      <label for="invite-locale">{translate($lang, 'people.invite.language')}</label>
      <select id="invite-locale" bind:value={locale}>
        {#each LANGUAGES as l (l)}
          <option value={l}>{NATIVE_NAMES[l]}</option>
        {/each}
      </select>
    </div>
    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <div class="foot">
      <button type="button" class="btn secondary" onclick={onclose}>{translate($lang, 'common.cancel')}</button>
      <button type="submit" class="btn primary" disabled={busy || !emailOk}>{translate($lang, 'people.invite.send')}</button>
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

  .hint {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .choice-set {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    border: 0;
  }

  .choice {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
  }

  .alert.warn {
    color: var(--color-warning-fg);
    background: var(--color-warning-bg);
    border-color: transparent;
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }
</style>
