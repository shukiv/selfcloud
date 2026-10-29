<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { session } from '../../lib/session';
  import { formatBytes } from '../../lib/files/format';
  import { statNode } from '../../lib/files/api';
  import type { FolderAccess, Node } from '../../lib/files/types';
  import { createSpace, deleteSpace, getSpace, listSpaces, renameSpace } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import type { Space } from '../../lib/manage/types';
  import ShareDialog from '../../components/share/ShareDialog.svelte';
  import SpaceSettings from '../../components/share/SpaceSettings.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import NameDialog from '../../components/manage/NameDialog.svelte';

  // Spaces: the team spaces of the account. Sharing a space goes through the share dialog on its root, the same
  // one the file browser uses. Deleting a space removes it and everything in it for everyone.
  let spaces = $state<Space[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let creating = $state(false);
  let renaming = $state<Space | null>(null);
  let deleting = $state<{ space: Space; files: number; bytes: number } | null>(null);
  let deleteError = $state<string | null>(null);
  let sharing = $state<{ node: Node; space: FolderAccess } | null>(null);
  let settings = $state<FolderAccess | null>(null);

  let account = $derived($session?.account?.id ?? '');

  onMount(() => {
    void load(true);
  });

  async function load(reset: boolean) {
    loading = true;
    try {
      const page = await listSpaces(account, reset ? undefined : cursor);
      spaces = reset ? page.items : [...spaces, ...page.items];
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // asAccess gives a space the shape the share dialog and the settings dialog read. An owner manages every space,
  // so the role is manager; the server still checks each change.
  function asAccess(s: Space): FolderAccess {
    return {
      id: s.id,
      name: s.name,
      kind: 'team',
      access: 'write',
      role: 'manager',
      hidden: false,
      members_only: s.members_only,
      sharing_managers_only: s.sharing_managers_only,
      no_download_for_viewers: s.no_download_for_viewers,
      allow_links: s.allow_links,
    };
  }

  async function openShare(s: Space) {
    error = null;
    try {
      const node = await statNode(s.root_node_id);
      sharing = { node, space: asAccess(s) };
    } catch (err) {
      error = failureText(err, $lang);
    }
  }

  // openDelete reads the space's totals first, so the confirmation can say what goes.
  async function openDelete(s: Space) {
    error = null;
    try {
      const d = await getSpace(account, s.id);
      deleteError = null;
      deleting = { space: s, files: d.files, bytes: d.bytes };
    } catch (err) {
      error = failureText(err, $lang);
    }
  }

  async function confirmDelete() {
    if (!deleting) return;
    deleteError = null;
    try {
      await deleteSpace(account, deleting.space.id);
      notice = translate($lang, 'spaces.deleted', { name: deleting.space.name });
      deleting = null;
      await load(true);
    } catch (err) {
      deleteError = failureText(err, $lang);
    }
  }
</script>

<svelte:head>
  <title>{translate($lang, 'manage.spaces.title')} · {$brand.name}</title>
</svelte:head>

<div class="toolbar">
  <h1 class="page-title">{translate($lang, 'manage.spaces.title')}</h1>
  <div class="toolbar-actions">
    <button type="button" class="btn primary" onclick={() => (creating = true)}>{translate($lang, 'spaces.new')}</button>
  </div>
</div>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && spaces.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if spaces.length === 0}
  <p class="empty" role="status">{translate($lang, 'spaces.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'manage.spaces.title')}>
    {#each spaces as s (s.id)}
      <li class="row">
        <div class="row-main">
          <p class="row-title"><bdi>{s.name}</bdi></p>
          {#if s.sharing_managers_only || s.members_only || s.no_download_for_viewers}
            <p class="row-meta">
              {[
                s.members_only ? translate($lang, 'spaces.flag.membersOnly') : '',
                s.sharing_managers_only ? translate($lang, 'spaces.flag.managersOnly') : '',
                s.no_download_for_viewers ? translate($lang, 'spaces.flag.noDownload') : '',
              ]
                .filter(Boolean)
                .join(' · ')}
            </p>
          {/if}
        </div>
        <div class="row-actions">
          <button type="button" class="btn secondary small" onclick={() => openShare(s)}>
            {translate($lang, 'spaces.share')}
            <span class="sr-only">: {s.name}</span>
          </button>
          <button type="button" class="btn secondary small" onclick={() => (settings = asAccess(s))}>
            {translate($lang, 'spaces.settings')}
            <span class="sr-only">: {s.name}</span>
          </button>
          <button type="button" class="btn secondary small" onclick={() => (renaming = s)}>
            {translate($lang, 'spaces.rename')}
            <span class="sr-only">: {s.name}</span>
          </button>
          <button type="button" class="btn secondary small danger-text" onclick={() => openDelete(s)}>
            {translate($lang, 'spaces.delete')}
            <span class="sr-only">: {s.name}</span>
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
  <NameDialog
    id="space-new"
    title={translate($lang, 'spaces.new')}
    label={translate($lang, 'spaces.nameLabel')}
    action={translate($lang, 'spaces.create')}
    onclose={() => (creating = false)}
    onsave={async (name) => {
      await createSpace(account, name);
      creating = false;
      notice = translate($lang, 'spaces.created', { name });
      await load(true);
    }}
  />
{/if}

{#if renaming}
  {@const s = renaming}
  <NameDialog
    id="space-rename"
    title={translate($lang, 'spaces.rename')}
    label={translate($lang, 'spaces.nameLabel')}
    initial={s.name}
    action={translate($lang, 'spaces.save')}
    onclose={() => (renaming = null)}
    onsave={async (name) => {
      await renameSpace(account, s.id, name);
      renaming = null;
      await load(true);
    }}
  />
{/if}

{#if deleting}
  {@const d = deleting}
  <Confirm
    id="space-delete"
    title={translate($lang, 'spaces.deleteTitle', { name: d.space.name })}
    message={`${translatePlural($lang, 'spaces.deleteFiles', d.files)} ${translate($lang, 'spaces.deleteSize', { size: formatBytes(d.bytes, $lang) })} ${translate($lang, 'spaces.deleteEveryone')}`}
    action={translate($lang, 'spaces.deleteAction')}
    danger
    requireName={d.space.name}
    error={deleteError}
    onconfirm={confirmDelete}
    onclose={() => (deleting = null)}
  />
{/if}

{#if sharing}
  <ShareDialog node={sharing.node} space={sharing.space} onclose={() => (sharing = null)} onchanged={() => undefined} />
{/if}

{#if settings}
  <SpaceSettings
    space={settings}
    onclose={() => (settings = null)}
    onsaved={() => void load(true)}
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

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
