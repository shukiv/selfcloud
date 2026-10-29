<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate } from '../../lib/i18n';
  import { formatDate } from '../../lib/files/format';
  import { session } from '../../lib/session';
  import { failureText } from '../../lib/manage/failure';
  import { listAccountLinks, PAGE_SIZE } from '../../lib/manage/api';
  import {
    audienceLabel,
    creatorLabel,
    folderLabel,
    itemLabel,
    kindLabel,
    mergeLinks,
    NO_FILTER,
    pathLabel,
    removeLink,
    statusKeys,
    type AccountLink,
    type AudienceFilter,
    type KindFilter,
    type Label,
    type LinkFilter,
  } from '../../lib/manage/links';
  import { deleteShareLink, getSharingPolicy } from '../../lib/sharing/api';
  import Confirm from '../../components/Confirm.svelte';
  import DeletePublicLinks from '../../components/share/DeletePublicLinks.svelte';

  // Links: every link of the account that anyone can open, and every drop link, with its place and its state.
  // An owner deletes one at a time, or all of them at once. Links that only people in the account can open are
  // on the items they sit on, and are not listed here.
  let rows = $state<AccountLink[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let filter = $state<LinkFilter>({ ...NO_FILTER });
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let anyoneCount = $state(0);
  let deleting = $state<AccountLink | null>(null);
  let deleteBusy = $state(false);
  let deleteError = $state<string | null>(null);
  let generation = 0;
  const LIMIT = PAGE_SIZE;

  let account = $derived($session?.account?.id ?? '');
  let viewerID = $derived($session?.user.id ?? '');

  onMount(() => {
    void load(true);
    void refreshCount();
  });

  // load reads a page; a reset starts from the first page again (after a filter change or a delete). A page that
  // comes back after a newer request started is dropped, so the filter the person last chose is the one shown.
  async function load(reset: boolean): Promise<void> {
    const my = ++generation;
    loading = true;
    try {
      const page = await listAccountLinks(account, filter, reset ? undefined : cursor, LIMIT);
      if (my !== generation) return;
      rows = mergeLinks(rows, page, reset);
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      if (my !== generation) return;
      error = failureText(err, $lang);
    } finally {
      if (my === generation) loading = false;
    }
  }

  async function refreshCount(): Promise<void> {
    try {
      anyoneCount = (await getSharingPolicy(account)).anyone_links;
    } catch {
      anyoneCount = 0;
    }
  }

  function filterChanged(): void {
    notice = null;
    cursor = undefined;
    void load(true);
  }

  function setAudience(v: string): void {
    filter = { ...filter, audience: v as AudienceFilter };
    filterChanged();
  }

  function setKind(v: string): void {
    filter = { ...filter, kind: v as KindFilter };
    filterChanged();
  }

  // text turns a label from lib/manage/links into shown text.
  function text(l: Label): string {
    if ('text' in l) return l.text;
    return translate($lang, l.key, l.params);
  }

  async function confirmDelete(): Promise<void> {
    if (!deleting) return;
    const link = deleting;
    deleteBusy = true;
    deleteError = null;
    try {
      await deleteShareLink(link.id);
      rows = removeLink(rows, link.id);
      deleting = null;
      notice = translate($lang, 'links.deleted');
      void refreshCount();
    } catch (err) {
      deleteError = failureText(err, $lang);
    } finally {
      deleteBusy = false;
    }
  }

  // deletedAll reloads after "Delete all public links"; the component already says how many went.
  function deletedAll(): void {
    cursor = undefined;
    void load(true);
    void refreshCount();
  }
</script>

<svelte:head>
  <title>{translate($lang, 'links.title')} · {$brand.name}</title>
</svelte:head>

<div class="toolbar">
  <h1 class="page-title">{translate($lang, 'links.title')}</h1>
  <div class="toolbar-actions">
    <label for="links-audience">{translate($lang, 'links.filter.audience')}</label>
    <select id="links-audience" value={filter.audience} onchange={(e) => setAudience((e.currentTarget as HTMLSelectElement).value)}>
      <option value="all">{translate($lang, 'links.filter.all')}</option>
      <option value="organisation">{translate($lang, 'links.audience.organisation')}</option>
      <option value="anyone">{translate($lang, 'links.audience.anyone')}</option>
    </select>
    <label for="links-kind">{translate($lang, 'links.filter.kind')}</label>
    <select id="links-kind" value={filter.kind} onchange={(e) => setKind((e.currentTarget as HTMLSelectElement).value)}>
      <option value="all">{translate($lang, 'links.filter.all')}</option>
      <option value="access">{translate($lang, 'links.kind.access')}</option>
      <option value="drop">{translate($lang, 'links.kind.drop')}</option>
    </select>
  </div>
</div>

<p class="hint">{translate($lang, 'links.intro')}</p>

<DeletePublicLinks account={account} count={anyoneCount} ondone={() => deletedAll()} />

{#if notice}
  <p class="hint" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && rows.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if rows.length === 0}
  <p class="empty" role="status">{translate($lang, 'links.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'links.title')}>
    {#each rows as l (l.id)}
      <li class="link-row">
        <div class="link-main">
          <p class="link-item">
            <bdi class="item-name">{text(itemLabel(l, false))}</bdi>
            {#if pathLabel(l)}
              <span class="muted small path">{pathLabel(l)}</span>
            {/if}
          </p>
          <p class="muted small">
            <span>{text(folderLabel(l, viewerID))}</span>
            <span aria-hidden="true">·</span>
            <span>{text(audienceLabel(l))}</span>
            <span aria-hidden="true">·</span>
            <span>{text(kindLabel(l))}</span>
            {#if l.role}
              <span aria-hidden="true">·</span>
              <span>{translate($lang, `role.${l.role}`)}</span>
            {/if}
          </p>
          <p class="muted small">
            <span>{text(creatorLabel(l))}</span>
            <span aria-hidden="true">·</span>
            <time datetime={l.created_at}>{formatDate(l.created_at, $lang)}</time>
            {#if l.expires_at}
              <span aria-hidden="true">·</span>
              <span>{translate($lang, 'links.ends', { date: formatDate(l.expires_at, $lang) })}</span>
            {:else}
              <span aria-hidden="true">·</span>
              <span>{translate($lang, 'links.noEnd')}</span>
            {/if}
            {#each statusKeys(l) as s (s)}
              <span aria-hidden="true">·</span>
              <span>{text(s)}</span>
            {/each}
          </p>
        </div>
        <div class="link-actions">
          <button type="button" class="btn secondary" onclick={() => { deleting = l; deleteError = null; }}>
            {translate($lang, 'links.delete.action')}
          </button>
        </div>
      </li>
    {/each}
  </ul>
{/if}
{#if cursor}
  <div class="more">
    <button type="button" class="btn secondary" disabled={loading} onclick={() => load(false)}>{translate($lang, 'common.loadMore')}</button>
  </div>
{/if}

{#if deleting}
  <Confirm
    id="link-delete"
    title={translate($lang, 'links.delete.title')}
    message={translate($lang, 'links.delete.message')}
    action={translate($lang, 'links.delete.action')}
    danger
    busy={deleteBusy}
    error={deleteError}
    onconfirm={() => void confirmDelete()}
    onclose={() => (deleting = null)}
  />
{/if}

<style>
  .toolbar {
    margin-block-end: var(--space-4);
  }

  .toolbar-actions select {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
    max-inline-size: 100%;
  }

  .link-row {
    display: flex;
    gap: var(--space-3);
    justify-content: space-between;
    align-items: start;
    padding-block: var(--space-3);
    border-block-end: 1px solid var(--color-border);
  }

  .link-main {
    min-inline-size: 0;
  }

  .link-item {
    margin: 0;
    overflow-wrap: anywhere;
  }

  .item-name {
    font-weight: 600;
  }

  .path {
    margin-inline-start: var(--space-2);
  }

  .link-actions {
    flex: none;
  }

  .muted {
    color: var(--color-muted-foreground);
  }

  .small {
    font-size: var(--text-sm);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
