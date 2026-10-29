<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate } from '../../lib/i18n';
  import { session } from '../../lib/session';
  import {
    listPeople,
    resendVerification,
    sendPasswordReset,
    updatePerson,
  } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import { loadNames, NO_NAMES } from '../../lib/manage/names';
  import { personLabel, statusTone } from '../../lib/manage/people';
  import type { Person, PersonReply } from '../../lib/manage/types';
  import type { Names } from '../../lib/audit/sentence';
  import Badge from '../../components/Badge.svelte';
  import InviteDialog from '../../components/manage/InviteDialog.svelte';
  import PersonDialog from '../../components/manage/PersonDialog.svelte';
  import RemoveDialog from '../../components/manage/RemoveDialog.svelte';

  // People: the account's owners and members, invited by email. An owner changes a person's role or status and
  // removes a person here. The server answers every change; a refusal is shown by its code and reason.
  let people = $state<Person[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let names = $state<Names>(NO_NAMES);
  let menuFor = $state<string | null>(null);
  let inviting = $state(false);
  let detail = $state<Person | null>(null);
  let removing = $state<Person | null>(null);
  let busyRow = $state<string | null>(null);

  let account = $derived($session?.account?.id ?? '');

  onMount(() => {
    void load(true);
    void loadNames(account).then((r) => (names = r.names)).catch(() => undefined);
  });

  async function load(reset: boolean) {
    loading = true;
    try {
      const page = await listPeople(account, reset ? undefined : cursor);
      people = reset ? page.items : [...people, ...page.items];
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // replace puts a changed person back in the list, so the row shows the answer the server gave.
  function replace(p: Person) {
    people = people.map((x) => (x.id === p.id ? { ...x, ...p } : x));
  }

  // sentNotice says whether a mail went out, by the answer's own flag.
  function sentNotice(reply: PersonReply): string {
    if (reply.verification?.sent === false) return translate($lang, 'people.mailNotSent');
    return translate($lang, 'people.mailSent');
  }

  async function act(p: Person, fn: () => Promise<void>) {
    menuFor = null;
    busyRow = p.id;
    error = null;
    notice = null;
    try {
      await fn();
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busyRow = null;
    }
  }

  const resend = (p: Person) =>
    act(p, async () => {
      const reply = await resendVerification(account, p.id, 'email');
      notice = sentNotice(reply);
    });

  const reset = (p: Person) =>
    act(p, async () => {
      const reply = await sendPasswordReset(account, p.id);
      notice = sentNotice(reply);
    });

  const changeRole = (p: Person) =>
    act(p, async () => {
      const role = p.role === 'owner' ? 'member' : 'owner';
      replace(await updatePerson(account, p.id, { role }));
    });

  const changeStatus = (p: Person) =>
    act(p, async () => {
      const status = p.status === 'disabled' ? 'active' : 'disabled';
      replace(await updatePerson(account, p.id, { status }));
    });

  function invited(reply: PersonReply) {
    inviting = false;
    notice = sentNotice(reply);
    void load(true);
  }

  function removed() {
    const name = removing ? personLabel(removing) : '';
    removing = null;
    notice = translate($lang, 'people.removed', { name });
    void load(true);
  }

  // closeMenu closes an open row menu when the press lands outside it.
  function closeMenu(event: MouseEvent) {
    if (menuFor !== null && !(event.target as Element).closest('.menu-wrap')) menuFor = null;
  }
</script>

<svelte:head>
  <title>{translate($lang, 'manage.people.title')} · {$brand.name}</title>
</svelte:head>

<svelte:window onclick={closeMenu} />

<div class="toolbar">
  <h1 class="page-title">{translate($lang, 'manage.people.title')}</h1>
  <div class="toolbar-actions">
    <button type="button" class="btn primary" onclick={() => (inviting = true)}>{translate($lang, 'people.invite.open')}</button>
  </div>
</div>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && people.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if people.length === 0}
  <p class="empty" role="status">{translate($lang, 'people.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'manage.people.title')}>
    {#each people as p (p.id)}
      <li class="row person-row">
        <div class="row-main">
          <p class="row-title"><bdi>{personLabel(p)}</bdi></p>
          <p class="row-meta"><bdi>{p.email}</bdi></p>
        </div>
        <div class="row-cell"><span class="pill">{translate($lang, `people.role.${p.role}`)}</span></div>
        <div class="row-cell"><Badge tone={statusTone(p.status)}>{translate($lang, `people.status.${p.status}`)}</Badge></div>
        <div class="row-actions">
          <button type="button" class="btn secondary small" onclick={() => (detail = p)}>
            {translate($lang, 'people.open')}
            <span class="sr-only">: {personLabel(p)}</span>
          </button>
          <div class="menu-wrap">
            <button
              type="button"
              class="btn secondary small"
              aria-haspopup="menu"
              aria-expanded={menuFor === p.id}
              aria-label={translate($lang, 'people.actionsFor', { name: personLabel(p) })}
              disabled={busyRow === p.id}
              onclick={() => (menuFor = menuFor === p.id ? null : p.id)}
            >
              …
            </button>
            {#if menuFor === p.id}
              <div class="menu person-menu" role="menu">
                {#if p.status === 'pending'}
                  <button type="button" role="menuitem" class="menu-item" onclick={() => resend(p)}>{translate($lang, 'people.resend')}</button>
                {/if}
                {#if p.status === 'active'}
                  <button type="button" role="menuitem" class="menu-item" onclick={() => reset(p)}>{translate($lang, 'people.reset')}</button>
                {/if}
                <button type="button" role="menuitem" class="menu-item" onclick={() => changeRole(p)}>
                  {p.role === 'owner' ? translate($lang, 'people.makeMember') : translate($lang, 'people.makeOwner')}
                </button>
                {#if p.status !== 'pending'}
                  <button type="button" role="menuitem" class="menu-item" onclick={() => changeStatus(p)}>
                    {p.status === 'disabled' ? translate($lang, 'people.enable') : translate($lang, 'people.disable')}
                  </button>
                {/if}
                <button type="button" role="menuitem" class="menu-item danger" onclick={() => { menuFor = null; removing = p; }}>
                  {translate($lang, 'people.remove.open')}
                </button>
              </div>
            {/if}
          </div>
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

{#if inviting}
  <InviteDialog account={account} onclose={() => (inviting = false)} oncreated={invited} />
{/if}

{#if detail}
  <PersonDialog account={account} person={detail} names={names} onclose={() => (detail = null)} />
{/if}

{#if removing}
  <RemoveDialog
    account={account}
    person={removing}
    candidates={people}
    onclose={() => (removing = null)}
    ondone={removed}
  />
{/if}

<style>
  .toolbar {
    margin-block-end: var(--space-4);
  }

  .person-row {
    align-items: center;
  }

  .row-meta {
    margin: 0;
  }

  .pill {
    font-size: var(--text-xs);
    font-weight: 500;
    padding: 0 var(--space-2);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-full);
  }

  .menu-wrap {
    position: relative;
  }

  .person-menu {
    inset-block-start: 100%;
    inset-inline-end: 0;
    min-width: 220px;
  }

  .menu-item.danger {
    color: var(--color-danger);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
