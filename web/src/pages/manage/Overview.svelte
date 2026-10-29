<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { session } from '../../lib/session';
  import { formatNumber } from '../../lib/files/format';
  import { auditLog, listSpaces, usage } from '../../lib/manage/api';
  import { collectAll } from '../../lib/manage/paging';
  import { loadNames, NO_NAMES } from '../../lib/manage/names';
  import { failureText } from '../../lib/manage/failure';
  import type { AuditEntry, Names } from '../../lib/audit/sentence';
  import type { Usage } from '../../lib/manage/types';
  import UsageMeter from '../../components/UsageMeter.svelte';
  import AuditItem from '../../components/manage/AuditItem.svelte';
  import PublicLinksSwitch from '../../components/share/PublicLinksSwitch.svelte';

  // Overview: the tenant's usage and people, its recent activity, and the next steps while it is new.
  let usageNow = $state<Usage | null>(null);
  let spaceCount = $state(0);
  let activity = $state<Array<AuditEntry & { id: string; time: string }>>([]);
  let names = $state<Names>(NO_NAMES);
  let error = $state<string | null>(null);
  let loading = $state(true);

  let account = $derived($session?.account?.id ?? '');

  onMount(() => {
    void load();
  });

  async function load() {
    loading = true;
    try {
      const [u, spaces, log, looked] = await Promise.all([
        usage(account),
        collectAll((c) => listSpaces(account, c)),
        auditLog(account, { limit: 10 }),
        loadNames(account),
      ]);
      usageNow = u;
      spaceCount = spaces.items.length;
      activity = log.items;
      names = looked.names;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  let people = $derived(usageNow ? usageNow.users_active + usageNow.users_pending : 0);
  let fresh = $derived(usageNow !== null && people <= 1 && spaceCount === 0);
</script>

<svelte:head>
  <title>{translate($lang, 'manage.overview.title')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'manage.overview.title')}</h1>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if usageNow}
  <section class="panel-plain" aria-labelledby="usage-title">
    <h2 id="usage-title">{translate($lang, 'manage.overview.usage')}</h2>
    <UsageMeter used={usageNow.used_bytes} quota={usageNow.quota_bytes} />
  </section>

  <dl class="stat-grid">
    <div class="stat">
      <dt>{translate($lang, 'manage.overview.people')}</dt>
      <dd>{formatNumber(usageNow.users_active, $lang)}</dd>
      <dd class="muted">{translatePlural($lang, 'manage.overview.pending', usageNow.users_pending)}</dd>
    </div>
    <div class="stat">
      <dt>{translate($lang, 'manage.overview.spaces')}</dt>
      <dd>{formatNumber(spaceCount, $lang)}</dd>
    </div>
    <div class="stat">
      <dt>{translate($lang, 'manage.overview.files')}</dt>
      <dd>{formatNumber(usageNow.files, $lang)}</dd>
    </div>
  </dl>

  {#if account}
    <PublicLinksSwitch {account} />
    <p class="hint"><a href="/manage/links">{translate($lang, 'links.openList')}</a></p>
  {/if}

  {#if fresh}
    <section class="panel-plain" aria-labelledby="next-title">
      <h2 id="next-title">{translate($lang, 'manage.overview.next')}</h2>
      <p class="toolbar-actions">
        <a class="btn primary" href="/manage/people">{translate($lang, 'manage.overview.inviteFirst')}</a>
        <a class="btn secondary" href="/manage/spaces">{translate($lang, 'manage.overview.createSpace')}</a>
      </p>
    </section>
  {/if}

  <section class="activity" aria-labelledby="activity-title">
    <div class="toolbar">
      <h2 id="activity-title" class="section-title">{translate($lang, 'manage.overview.activity')}</h2>
      <a href="/manage/audit">{translate($lang, 'manage.overview.fullLog')}</a>
    </div>
    {#if activity.length === 0}
      <p class="empty">{translate($lang, 'manage.overview.noActivity')}</p>
    {:else}
      <ul class="rows">
        {#each activity as entry (entry.id)}
          <AuditItem {entry} names={names} />
        {/each}
      </ul>
    {/if}
  </section>
{/if}

<style>
  .section-title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
  }

  .activity {
    margin-block-start: var(--space-6);
  }

  .muted {
    color: var(--color-muted-foreground);
  }
</style>
