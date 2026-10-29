<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate } from '../../lib/i18n';
  import { listPeople, resendVerification } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import { personLabel, statusTone } from '../../lib/manage/people';
  import type { Person, PersonReply } from '../../lib/manage/types';
  import Badge from '../Badge.svelte';
  import ShowOnce from './ShowOnce.svelte';

  // TenantUsers: the tenant's people, read only. An admin does not manage a tenant's members day to day; it can
  // resend the invitation of an owner who has not verified yet, by email or as a new link shown once.
  let { account }: { account: string } = $props();

  let people = $state<Person[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let link = $state<{ url: string; who: string } | null>(null);
  let busy = $state<string | null>(null);

  onMount(() => {
    void load(true);
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

  async function resend(p: Person, method: 'email' | 'link') {
    busy = p.id;
    error = null;
    notice = null;
    try {
      const reply: PersonReply = await resendVerification(account, p.id, method);
      if (method === 'link' && reply.verification?.url) {
        link = { url: reply.verification.url, who: personLabel(p) };
      } else {
        notice = reply.verification?.sent === false ? translate($lang, 'people.mailNotSent') : translate($lang, 'people.mailSent');
      }
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busy = null;
    }
  }
</script>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}
{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}

{#if loading && people.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if people.length === 0}
  <p class="empty" role="status">{translate($lang, 'tenant.users.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'tenant.users.title')}>
    {#each people as p (p.id)}
      <li class="row">
        <div class="row-main">
          <p class="row-title"><bdi>{personLabel(p)}</bdi></p>
          <p class="row-meta"><bdi>{p.email}</bdi></p>
        </div>
        <div class="row-cell">{translate($lang, `people.role.${p.role}`)}</div>
        <div class="row-cell"><Badge tone={statusTone(p.status)}>{translate($lang, `people.status.${p.status}`)}</Badge></div>
        {#if p.role === 'owner' && p.status === 'pending'}
          <div class="row-actions">
            <button type="button" class="btn secondary small" disabled={busy === p.id} onclick={() => resend(p, 'email')}>
              {translate($lang, 'tenant.users.resend')}
              <span class="sr-only">: {personLabel(p)}</span>
            </button>
            <button type="button" class="btn secondary small" disabled={busy === p.id} onclick={() => resend(p, 'link')}>
              {translate($lang, 'tenant.users.newLink')}
              <span class="sr-only">: {personLabel(p)}</span>
            </button>
          </div>
        {/if}
      </li>
    {/each}
  </ul>
  {#if cursor}
    <div class="more">
      <button type="button" class="btn secondary" disabled={loading} onclick={() => load(false)}>{translate($lang, 'common.loadMore')}</button>
    </div>
  {/if}
{/if}

{#if link}
  {@const l = link}
  <ShowOnce
    id="owner-link"
    title={translate($lang, 'tenant.users.linkTitle', { name: l.who })}
    label={translate($lang, 'tenant.new.linkLabel')}
    value={l.url}
    warning={translate($lang, 'tenant.new.linkWarning')}
    onclosed={() => (link = null)}
  />
{/if}

<style>
  .row-meta {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
