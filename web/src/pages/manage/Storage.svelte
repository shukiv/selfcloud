<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { session } from '../../lib/session';
  import { formatBytes, formatDate, formatNumber } from '../../lib/files/format';
  import { failureText } from '../../lib/manage/failure';
  import { personLabel } from '../../lib/manage/people';
  import {
    accountLargest,
    accountStorage,
    setPersonLimit,
    setPolicyDefault,
    type AccountStorage,
    type SpaceFile,
    type StoragePerson,
  } from '../../lib/storage/api';
  import { heldBytes, LARGEST_PAGE, mergePage, PEOPLE_PAGE, sortByUse } from '../../lib/storage/logic';
  import UsageMeter from '../../components/UsageMeter.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import Badge from '../../components/Badge.svelte';
  import LimitEditor from '../../components/storage/LimitEditor.svelte';

  // Manage > Storage: the account's meter, the default limit for each person, the people by use with their own
  // limits, and the team spaces with their largest files. An owner sees per-person totals but never a file name from
  // a person's My files; the server leaves those out.
  let data = $state<AccountStorage | null>(null);
  let people = $state<StoragePerson[]>([]);
  let usersCursor = $state<string | null>(null);
  let largest = $state<SpaceFile[]>([]);
  let largestCursor = $state<string | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let editingDefault = $state(false);
  let editingPerson = $state<StoragePerson | null>(null);

  let account = $derived($session?.account?.id ?? '');
  let folders = $derived((data?.folders ?? []).filter((f) => f.kind === 'team'));

  onMount(() => {
    void load();
  });

  async function load() {
    loading = true;
    try {
      const [s, l] = await Promise.all([accountStorage(account, null, PEOPLE_PAGE), accountLargest(account, { limit: LARGEST_PAGE })]);
      data = s;
      people = sortByUse(s.users ?? []);
      usersCursor = s.users_next_cursor ?? null;
      largest = l.items;
      largestCursor = l.next_cursor ?? null;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // refreshAccount reads the account block again after a change, keeping the rows already shown.
  async function refreshAccount() {
    const s = await accountStorage(account, null, PEOPLE_PAGE);
    data = s;
  }

  async function morePeople() {
    try {
      const s = await accountStorage(account, usersCursor, PEOPLE_PAGE);
      people = sortByUse(mergePage(people, s.users ?? []));
      usersCursor = s.users_next_cursor ?? null;
    } catch (err) {
      error = failureText(err, $lang);
    }
  }

  async function moreLargest() {
    try {
      const l = await accountLargest(account, { limit: LARGEST_PAGE, cursor: largestCursor });
      largest = mergePage(largest, l.items);
      largestCursor = l.next_cursor ?? null;
    } catch (err) {
      error = failureText(err, $lang);
    }
  }

  async function saveDefault(value: number) {
    await setPolicyDefault(account, value);
    editingDefault = false;
    notice = translate($lang, 'storage.limitSaved');
    await refreshAccount();
  }

  async function savePerson(value: number | null) {
    const p = editingPerson;
    if (!p) return;
    await setPersonLimit(account, p.id, value);
    editingPerson = null;
    notice = translate($lang, 'storage.limitSaved');
    // The person's row is read again, so it shows the limit the server now applies.
    const s = await accountStorage(account, null, PEOPLE_PAGE);
    data = s;
    people = sortByUse(s.users ?? []);
    usersCursor = s.users_next_cursor ?? null;
  }

  function limitText(p: StoragePerson): string {
    return p.effective_quota_bytes === null ? translate($lang, 'storage.noLimit') : formatBytes(p.effective_quota_bytes, $lang);
  }

  let held = $derived(data ? heldBytes(data.account) : 0);
</script>

<svelte:head>
  <title>{translate($lang, 'manage.storage.title')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'manage.storage.title')}</h1>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && !data}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if data}
  <section class="panel-plain" aria-labelledby="account-storage">
    <h2 id="account-storage">{translate($lang, 'storage.account')}</h2>
    <UsageMeter used={held} quota={data.account.quota_bytes} />
    <dl class="stat-grid">
      <div class="stat">
        <dt>{translate($lang, 'storage.reserved')}</dt>
        <dd>{formatBytes(data.account.reserved_bytes, $lang)}</dd>
      </div>
      <div class="stat">
        <dt>{translate($lang, 'storage.spacesCount')}</dt>
        <dd>{formatNumber(folders.length, $lang)}</dd>
      </div>
    </dl>
  </section>

  <section class="panel-plain" aria-labelledby="default-limit">
    <h2 id="default-limit">{translate($lang, 'storage.defaultTitle')}</h2>
    <div class="line">
      <p>
        {data.account.user_quota_bytes === 0
          ? translate($lang, 'storage.defaultNone')
          : translate($lang, 'storage.defaultLine', { size: formatBytes(data.account.user_quota_bytes, $lang) })}
      </p>
      <button type="button" class="btn secondary small" onclick={() => (editingDefault = true)}>{translate($lang, 'storage.change')}</button>
    </div>
  </section>

  <section class="block" id="people-storage" aria-labelledby="people-storage-title">
    <h2 id="people-storage-title" class="section-title">{translate($lang, 'storage.people')}</h2>
    {#if people.length === 0}
      <p class="empty" role="status">{translate($lang, 'storage.peopleEmpty')}</p>
    {:else}
      <table class="storage-table">
        <thead>
          <tr>
            <th scope="col">{translate($lang, 'storage.person')}</th>
            <th scope="col" class="num">{translate($lang, 'storage.used')}</th>
            <th scope="col">{translate($lang, 'storage.limit')}</th>
            <th scope="col"><span class="sr-only">{translate($lang, 'storage.actions')}</span></th>
          </tr>
        </thead>
        <tbody>
          {#each people as p (p.id)}
            <tr>
              <th scope="row">
                <bdi>{personLabel(p)}</bdi>
                {#if p.email && p.email !== personLabel(p)}<br /><span class="muted"><bdi>{p.email}</bdi></span>{/if}
              </th>
              <td class="num">{formatBytes(heldBytes(p), $lang)}</td>
              <td>
                {limitText(p)}
                {#if p.quota_bytes === null}<span class="muted"> · {translate($lang, 'storage.followsDefault')}</span>{/if}
              </td>
              <td class="end">
                <button type="button" class="btn secondary small" onclick={() => (editingPerson = p)}>
                  {translate($lang, 'storage.change')}
                  <span class="sr-only">: {personLabel(p)}</span>
                </button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if usersCursor}
        <div class="more">
          <button type="button" class="btn secondary" onclick={morePeople}>{translate($lang, 'common.loadMore')}</button>
        </div>
      {/if}
    {/if}
  </section>

  <section class="block" id="spaces-storage" aria-labelledby="spaces-storage-title">
    <h2 id="spaces-storage-title" class="section-title">{translate($lang, 'storage.spaces')}</h2>
    {#if folders.length === 0}
      <p class="empty" role="status">{translate($lang, 'storage.spacesEmpty')}</p>
    {:else}
      <table class="storage-table">
        <thead>
          <tr>
            <th scope="col">{translate($lang, 'storage.space')}</th>
            <th scope="col" class="num">{translate($lang, 'storage.size')}</th>
            <th scope="col" class="num">{translate($lang, 'storage.files')}</th>
          </tr>
        </thead>
        <tbody>
          {#each folders as f (f.id)}
            <tr>
              <th scope="row"><bdi>{f.name ?? ''}</bdi></th>
              <td class="num">{formatBytes(f.used_bytes + f.reserved_bytes, $lang)}</td>
              <td class="num">{translatePlural($lang, 'storage.fileCount', f.used_files)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>

  <section class="block" id="largest-spaces" aria-labelledby="largest-spaces-title">
    <h2 id="largest-spaces-title" class="section-title">{translate($lang, 'storage.spaceLargest')}</h2>
    {#if largest.length === 0}
      <p class="empty" role="status">{translate($lang, 'storage.largestEmpty')}</p>
    {:else}
      <table class="storage-table">
        <thead>
          <tr>
            <th scope="col">{translate($lang, 'storage.name')}</th>
            <th scope="col">{translate($lang, 'storage.space')}</th>
            <th scope="col" class="num">{translate($lang, 'storage.size')}</th>
            <th scope="col">{translate($lang, 'storage.modified')}</th>
          </tr>
        </thead>
        <tbody>
          {#each largest as f (f.id)}
            <tr>
              <th scope="row">
                <bdi>{f.name}</bdi>
                {#if f.in_trash}<span class="inline-badge"><Badge tone="warn">{translate($lang, 'storage.inTrash')}</Badge></span>{/if}
              </th>
              <td><bdi>{f.folder_name}</bdi></td>
              <td class="num">{formatBytes(f.size, $lang)}</td>
              <td><time datetime={f.modified_at}>{formatDate(f.modified_at, $lang)}</time></td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if largestCursor}
        <div class="more">
          <button type="button" class="btn secondary" onclick={moreLargest}>{translate($lang, 'common.loadMore')}</button>
        </div>
      {/if}
    {/if}
  </section>
{/if}

{#if editingDefault && data}
  <Dialog title={translate($lang, 'storage.defaultTitle')} id="default-limit-dialog" onclose={() => (editingDefault = false)}>
    <LimitEditor
      current={data.account.user_quota_bytes === 0 ? null : data.account.user_quota_bytes}
      allowDefault={false}
      onsave={(v) => saveDefault(v ?? 0)}
      oncancel={() => (editingDefault = false)}
    />
  </Dialog>
{/if}

{#if editingPerson}
  <Dialog
    title={translate($lang, 'storage.limitFor', { name: personLabel(editingPerson) })}
    id="person-limit-dialog"
    onclose={() => (editingPerson = null)}
  >
    <LimitEditor
      current={editingPerson.quota_bytes}
      allowDefault={true}
      onsave={savePerson}
      oncancel={() => (editingPerson = null)}
    />
  </Dialog>
{/if}

<style>
  .section-title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
  }

  section {
    margin-block-start: var(--space-6);
  }

  .line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .line p,
  .storage-table th {
    margin: 0;
  }

  .muted {
    color: var(--color-muted-foreground);
  }

  .storage-table {
    width: 100%;
    border-collapse: collapse;
    font-variant-numeric: tabular-nums;
  }

  .storage-table th,
  .storage-table td {
    padding: var(--space-2) var(--space-3);
    text-align: start;
    border-block-end: 1px solid var(--color-muted);
    vertical-align: top;
  }

  .storage-table th[scope='row'] {
    font-weight: 500;
  }

  .storage-table .num {
    text-align: end;
    white-space: nowrap;
  }

  .storage-table .end {
    text-align: end;
  }

  .inline-badge {
    margin-inline-start: var(--space-2);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
