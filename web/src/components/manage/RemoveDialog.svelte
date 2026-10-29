<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { ApiError } from '../../lib/apiError';
  import { lang, translate } from '../../lib/i18n';
  import { failureText } from '../../lib/manage/failure';
  import { personLabel } from '../../lib/manage/people';
  import { removePerson } from '../../lib/manage/api';
  import type { Person } from '../../lib/manage/types';
  import Confirm from '../Confirm.svelte';
  import Dialog from '../Dialog.svelte';

  // RemoveDialog removes a person. When the person's My files hold files, the server refuses until the owner says
  // what happens to them: they move to another active member, or they are deleted for good. The server does not
  // say how many files there are, and neither does this dialog.
  let {
    account,
    person,
    candidates,
    onclose,
    ondone,
  }: {
    account: string;
    person: Person;
    candidates: Person[];
    onclose: () => void;
    ondone: () => void;
  } = $props();

  let step = $state<'confirm' | 'files'>('confirm');
  let choice = $state<'move' | 'delete'>('move');
  let target = $state('');
  let busy = $state(false);
  let error = $state<string | null>(null);
  let label = $derived(personLabel(person));
  let movable = $derived(candidates.filter((c) => c.id !== person.id && c.role === 'member' && c.status === 'active'));
  let canGo = $derived(choice === 'delete' || target !== '');

  async function go(files?: { transfer_to?: string; delete_files?: boolean }) {
    busy = true;
    error = null;
    try {
      await removePerson(account, person.id, files);
      ondone();
    } catch (err) {
      // A conflict with no reason is the files question: the server did not remove the person.
      if (step === 'confirm' && err instanceof ApiError && err.code === 'conflict' && typeof err.details.reason !== 'string') {
        step = 'files';
      } else {
        error = failureText(err, $lang);
      }
    } finally {
      busy = false;
    }
  }

  function submit() {
    if (step === 'confirm') return void go();
    if (choice === 'move') return void go({ transfer_to: target });
    return void go({ delete_files: true });
  }
</script>

{#if step === 'confirm'}
  <Confirm
    id="remove"
    title={translate($lang, 'people.remove.title', { name: label })}
    message={translate($lang, 'people.remove.message', { name: label })}
    action={translate($lang, 'people.remove.action')}
    danger
    {busy}
    {error}
    onconfirm={submit}
    {onclose}
  />
{:else}
  <Dialog title={translate($lang, 'people.remove.filesTitle', { name: label })} id="remove-files" {onclose}>
  <form class="files-step" onsubmit={(e) => { e.preventDefault(); if (canGo) submit(); }}>
    <p>{translate($lang, 'people.remove.filesMessage')}</p>
    <fieldset class="choice-set">
      <legend class="sr-only">{translate($lang, 'people.remove.filesTitle', { name: label })}</legend>
      <label class="choice">
        <input type="radio" name="files" value="move" bind:group={choice} disabled={movable.length === 0} />
        <span>{translate($lang, 'people.remove.move')}</span>
      </label>
      {#if choice === 'move'}
        <div class="field">
          <label for="files-target">{translate($lang, 'people.remove.moveTo')}</label>
          <select id="files-target" bind:value={target}>
            <option value="">{translate($lang, 'people.remove.choose')}</option>
            {#each movable as c (c.id)}
              <option value={c.id}>{personLabel(c)}</option>
            {/each}
          </select>
        </div>
      {/if}
      <label class="choice">
        <input type="radio" name="files" value="delete" bind:group={choice} />
        <span>{translate($lang, 'people.remove.deleteFiles')}</span>
      </label>
    </fieldset>
    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <div class="foot">
      <button type="button" class="btn secondary" onclick={onclose}>{translate($lang, 'common.cancel')}</button>
      <button type="submit" class="btn {choice === 'delete' ? 'danger-fill' : 'primary'}" disabled={busy || !canGo}>
        {translate($lang, 'people.remove.action')}
      </button>
    </div>
  </form>
  </Dialog>
{/if}

<style>
  .files-step {
    display: grid;
    gap: var(--space-3);
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

  .field {
    display: grid;
    gap: var(--space-1);
  }

  .field select {
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
