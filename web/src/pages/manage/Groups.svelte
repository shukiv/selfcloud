<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate } from '../../lib/i18n';
  import { session } from '../../lib/session';
  import type { DirectoryEntry } from '../../lib/files/types';
  import {
    addGroupMember,
    createGroup,
    deleteGroup,
    listGroupMembers,
    listGroups,
    removeGroupMember,
    renameGroup,
  } from '../../lib/manage/api';
  import { collectAll } from '../../lib/manage/paging';
  import { failureText } from '../../lib/manage/failure';
  import { loadNames, NO_NAMES } from '../../lib/manage/names';
  import type { Group } from '../../lib/manage/types';
  import type { Names } from '../../lib/audit/sentence';
  import PeopleCombobox from '../../components/share/PeopleCombobox.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import NameDialog from '../../components/manage/NameDialog.svelte';

  // Groups: a group is a named set of people. A permission on a team space given to a group reaches each member
  // at once, and removing a member ends that access at once.
  let groups = $state<Group[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let creating = $state(false);
  let renaming = $state<Group | null>(null);
  let deleting = $state<Group | null>(null);
  let deleteError = $state<string | null>(null);
  let open = $state<Group | null>(null);
  let members = $state<string[]>([]);
  let names = $state<Names>(NO_NAMES);
  let membersError = $state<string | null>(null);
  let adding = $state<DirectoryEntry | null>(null);
  let comboKey = $state(0);

  let account = $derived($session?.account?.id ?? '');

  onMount(() => {
    void load(true);
    void loadNames(account).then((r) => (names = r.names)).catch(() => undefined);
  });

  async function load(reset: boolean) {
    loading = true;
    try {
      const page = await listGroups(account, reset ? undefined : cursor);
      groups = reset ? page.items : [...groups, ...page.items];
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  async function showMembers(g: Group) {
    open = g;
    membersError = null;
    members = [];
    try {
      const all = await collectAll((c) => listGroupMembers(account, g.id, c));
      members = all.items.map((m) => m.user_id);
    } catch (err) {
      membersError = failureText(err, $lang);
    }
  }

  // personText names a member from the lookup, or shows a neutral word for a person the page has not read.
  function personText(id: string): string {
    return names.person(id) ?? translate($lang, 'people.unknown');
  }

  async function addMember() {
    if (!open || !adding) return;
    membersError = null;
    try {
      await addGroupMember(account, open.id, adding.id);
      adding = null;
      comboKey++;
      await showMembers(open);
    } catch (err) {
      membersError = failureText(err, $lang);
    }
  }

  async function removeMember(usr: string) {
    if (!open) return;
    membersError = null;
    try {
      await removeGroupMember(account, open.id, usr);
      await showMembers(open);
    } catch (err) {
      membersError = failureText(err, $lang);
    }
  }

  async function confirmDelete() {
    if (!deleting) return;
    deleteError = null;
    try {
      await deleteGroup(account, deleting.id);
      if (open?.id === deleting.id) open = null;
      notice = translate($lang, 'groups.deleted', { name: deleting.name });
      deleting = null;
      await load(true);
    } catch (err) {
      deleteError = failureText(err, $lang);
    }
  }
</script>

<svelte:head>
  <title>{translate($lang, 'manage.groups.title')} · {$brand.name}</title>
</svelte:head>

<div class="toolbar">
  <h1 class="page-title">{translate($lang, 'manage.groups.title')}</h1>
  <div class="toolbar-actions">
    <button type="button" class="btn primary" onclick={() => (creating = true)}>{translate($lang, 'groups.new')}</button>
  </div>
</div>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && groups.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if groups.length === 0}
  <p class="empty" role="status">{translate($lang, 'groups.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'manage.groups.title')}>
    {#each groups as g (g.id)}
      <li class="row">
        <div class="row-main"><p class="row-title"><bdi>{g.name}</bdi></p></div>
        <div class="row-actions">
          <button type="button" class="btn secondary small" aria-pressed={open?.id === g.id} onclick={() => showMembers(g)}>
            {translate($lang, 'groups.members')}
            <span class="sr-only">: {g.name}</span>
          </button>
          <button type="button" class="btn secondary small" onclick={() => (renaming = g)}>
            {translate($lang, 'groups.rename')}
            <span class="sr-only">: {g.name}</span>
          </button>
          <button type="button" class="btn secondary small" onclick={() => (deleting = g)}>
            {translate($lang, 'groups.delete')}
            <span class="sr-only">: {g.name}</span>
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

{#if open}
  <section class="panel-plain members" aria-labelledby="members-title">
    <h2 id="members-title"><bdi>{open.name}</bdi></h2>
    {#if membersError}
      <p class="alert error" role="alert">{membersError}</p>
    {/if}
    {#if members.length === 0}
      <p class="muted">{translate($lang, 'groups.noMembers')}</p>
    {:else}
      <ul class="rows">
        {#each members as usr (usr)}
          <li class="row">
            <div class="row-main"><p class="row-title"><bdi>{personText(usr)}</bdi></p></div>
            <div class="row-actions">
              <button type="button" class="btn secondary small" onclick={() => removeMember(usr)}>
                {translate($lang, 'groups.removeMember')}
                <span class="sr-only">: {personText(usr)}</span>
              </button>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
    {#key comboKey}
      <PeopleCombobox
        id="group-add"
        kind="user"
        label={translate($lang, 'groups.addPerson')}
        hint={translate($lang, 'groups.addHint')}
        onpick={(entry) => (adding = entry)}
      />
    {/key}
    <div class="toolbar-actions">
      <button type="button" class="btn primary" disabled={!adding} onclick={addMember}>{translate($lang, 'groups.add')}</button>
    </div>
  </section>
{/if}

{#if creating}
  <NameDialog
    id="group-new"
    title={translate($lang, 'groups.new')}
    label={translate($lang, 'groups.nameLabel')}
    action={translate($lang, 'groups.create')}
    onclose={() => (creating = false)}
    onsave={async (name) => {
      await createGroup(account, name);
      creating = false;
      await load(true);
    }}
  />
{/if}

{#if renaming}
  {@const g = renaming}
  <NameDialog
    id="group-rename"
    title={translate($lang, 'groups.rename')}
    label={translate($lang, 'groups.nameLabel')}
    initial={g.name}
    action={translate($lang, 'groups.save')}
    onclose={() => (renaming = null)}
    onsave={async (name) => {
      await renameGroup(account, g.id, name);
      renaming = null;
      await load(true);
    }}
  />
{/if}

{#if deleting}
  <Confirm
    id="group-delete"
    title={translate($lang, 'groups.deleteTitle', { name: deleting.name })}
    message={translate($lang, 'groups.deleteMessage')}
    action={translate($lang, 'groups.delete')}
    danger
    error={deleteError}
    onconfirm={confirmDelete}
    onclose={() => (deleting = null)}
  />
{/if}

<style>
  .toolbar {
    margin-block-end: var(--space-4);
  }

  .members {
    margin-block-start: var(--space-6);
  }

  .members h2 {
    font-size: var(--text-lg);
  }

  .muted {
    margin: 0;
    color: var(--color-muted-foreground);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
