<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate } from '../../lib/i18n';
  import { createAdmin, deleteAdmin, listAdmins } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import { isLastActiveAdmin } from '../../lib/manage/admin';
  import { personLabel, statusTone } from '../../lib/manage/people';
  import type { Person, PersonReply } from '../../lib/manage/types';
  import Badge from '../../components/Badge.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import ShowOnce from '../../components/admin/ShowOnce.svelte';

  // Admins: the people who run this installation. The last active admin cannot be removed; the page says so, and
  // the server refuses it too.
  let admins = $state<Person[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let creating = $state(false);
  let removing = $state<Person | null>(null);
  let removeError = $state<string | null>(null);
  let secret = $state<{ url: string; who: string } | null>(null);

  let email = $state('');
  let name = $state('');
  let how = $state<'email' | 'link'>('email');
  let busy = $state(false);
  let formError = $state<string | null>(null);
  let emailOk = $derived(/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim()));

  onMount(() => {
    void load(true);
  });

  async function load(reset: boolean) {
    loading = true;
    try {
      const page = await listAdmins(reset ? undefined : cursor);
      admins = reset ? page.items : [...admins, ...page.items];
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    if (busy || !emailOk) return;
    busy = true;
    formError = null;
    try {
      const reply: PersonReply = await createAdmin({
        email: email.trim(),
        display_name: name.trim(),
        verification: how,
      });
      creating = false;
      email = '';
      name = '';
      if (reply.verification?.url) {
        secret = { url: reply.verification.url, who: personLabel(reply) };
      } else {
        notice = reply.verification?.sent === false ? translate($lang, 'people.mailNotSent') : translate($lang, 'admins.added');
      }
      await load(true);
    } catch (err) {
      formError = failureText(err, $lang);
    } finally {
      busy = false;
    }
  }

  async function remove() {
    if (!removing) return;
    removeError = null;
    try {
      await deleteAdmin(removing.id);
      notice = translate($lang, 'admins.removed', { name: personLabel(removing) });
      removing = null;
      await load(true);
    } catch (err) {
      removeError = failureText(err, $lang);
    }
  }
</script>

<svelte:head>
  <title>{translate($lang, 'admin.admins.title')} · {$brand.name}</title>
</svelte:head>

<div class="toolbar">
  <h1 class="page-title">{translate($lang, 'admin.admins.title')}</h1>
  <div class="toolbar-actions">
    <button type="button" class="btn primary" onclick={() => { formError = null; creating = true; }}>{translate($lang, 'admins.new')}</button>
  </div>
</div>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && admins.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'admin.admins.title')}>
    {#each admins as a (a.id)}
      {@const last = isLastActiveAdmin(admins, a.id)}
      <li class="row">
        <div class="row-main">
          <p class="row-title"><bdi>{personLabel(a)}</bdi></p>
          <p class="row-meta"><bdi>{a.email}</bdi></p>
          {#if last}
            <p class="row-meta" id="last-admin-{a.id}">{translate($lang, 'admins.lastHint')}</p>
          {/if}
        </div>
        <div class="row-cell"><Badge tone={statusTone(a.status)}>{translate($lang, `people.status.${a.status}`)}</Badge></div>
        <div class="row-actions">
          <button
            type="button"
            class="btn secondary small danger-text"
            disabled={last}
            aria-describedby={last ? `last-admin-${a.id}` : undefined}
            title={last ? translate($lang, 'admins.lastHint') : undefined}
            onclick={() => { removeError = null; removing = a; }}
          >
            {translate($lang, 'admins.remove')}
            <span class="sr-only">: {personLabel(a)}</span>
          </button>
        </div>
      </li>
    {/each}
  </ul>
  {#if cursor}
    <div class="more">
      <button type="button" class="btn secondary" disabled={loading} onclick={() => load(false)}>{translate($lang, 'common.loadMore')}</button>
    </div>
  {/if}
{/if}

{#if creating}
  <Dialog title={translate($lang, 'admins.new.title')} id="new-admin" wide onclose={() => (creating = false)}>
    <form class="form" onsubmit={add} novalidate>
      <div class="field">
        <label for="admin-email">{translate($lang, 'admins.new.email')}</label>
        <input id="admin-email" type="email" autocomplete="off" inputmode="email" bind:value={email} data-autofocus />
      </div>
      <div class="field">
        <label for="admin-name">{translate($lang, 'admins.new.name')}</label>
        <input id="admin-name" type="text" autocomplete="off" bind:value={name} />
      </div>
      <fieldset class="how">
        <legend>{translate($lang, 'tenant.new.how')}</legend>
        <label class="check"><input type="radio" name="admin-how" value="email" bind:group={how} /> <span>{translate($lang, 'tenant.new.howEmail')}</span></label>
        <label class="check"><input type="radio" name="admin-how" value="link" bind:group={how} /> <span>{translate($lang, 'tenant.new.howLink')}</span></label>
      </fieldset>
      {#if formError}
        <p class="alert error" role="alert">{formError}</p>
      {/if}
      <div class="foot">
        <button type="button" class="btn secondary" onclick={() => (creating = false)}>{translate($lang, 'common.cancel')}</button>
        <button type="submit" class="btn primary" disabled={busy || !emailOk}>{translate($lang, 'admins.new.send')}</button>
      </div>
    </form>
  </Dialog>
{/if}

{#if secret}
  {@const s = secret}
  <ShowOnce
    id="admin-link"
    title={translate($lang, 'admins.linkTitle', { name: s.who })}
    label={translate($lang, 'tenant.new.linkLabel')}
    value={s.url}
    warning={translate($lang, 'tenant.new.linkWarning')}
    onclosed={() => (secret = null)}
  />
{/if}

{#if removing}
  {@const r = removing}
  <Confirm
    id="remove-admin"
    title={translate($lang, 'admins.removeTitle', { name: personLabel(r) })}
    message={translate($lang, 'admins.removeMessage')}
    action={translate($lang, 'admins.remove')}
    danger
    error={removeError}
    onconfirm={remove}
    onclose={() => (removing = null)}
  />
{/if}

<style>
  .toolbar {
    margin-block-end: var(--space-4);
  }

  .row-meta {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .danger-text {
    color: var(--color-danger);
  }

  .form {
    display: grid;
    gap: var(--space-4);
  }

  .field,
  .how {
    display: grid;
    gap: var(--space-1);
    margin: 0;
    padding: 0;
    border: 0;
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

  .check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
