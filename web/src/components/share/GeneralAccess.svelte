<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import { session } from '../../lib/session';
  import { go } from '../../lib/router';
  import type { Node, Role } from '../../lib/files/types';
  import {
    createShareLink,
    deleteShareLink,
    listShareLinks,
    resetShareLink,
    updateShareLink,
  } from '../../lib/sharing/api';
  import { inheritedLinks, linkRoleOptions, ownLink, type ShareLink } from '../../lib/sharing/links';
  import { dateInputValue, expiryFromInput, expiryLimit, maxExpiryDays } from '../../lib/sharing/limits';

  // GeneralAccess is the "General access" part of the share dialog: whether the item has a link that the
  // account's members can open, and the link's role, end date and secret. The server checks every change;
  // this only shows what the caller may do. A link never gives manager.
  let {
    node,
    cap,
    onchanged,
    onopen,
  }: {
    node: Node;
    cap: Role | null;
    onchanged: () => void;
    onopen: () => void;
  } = $props();

  let links = $state<ShareLink[]>([]);
  let loading = $state(true);
  let busy = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  // madeURL is the URL of a link made or reset just now. It is shown once, so the person copies it now.
  let madeURL = $state<string | null>(null);
  let choice = $state<'restricted' | 'organisation'>('restricted');
  let newRole = $state<Role>('viewer');
  let newExpires = $state('');

  let own = $derived(ownLink(links, node.id));
  let above = $derived(inheritedLinks(links, node.id));
  let roles = $derived(linkRoleOptions(cap));
  let canChange = $derived(cap !== null);
  let account = $derived($session?.account?.name ?? '');
  let maxDays = $derived($maxExpiryDays);
  let latest = $derived(expiryLimit(maxDays, new Date()));
  let latestDate = $derived(latest ? dateInputValue(latest) : undefined);
  let earliestDate = $derived(dateInputValue(new Date(Date.now() + 24 * 60 * 60 * 1000)));

  onMount(() => {
    void load();
  });

  async function load(): Promise<void> {
    loading = true;
    try {
      const res = await listShareLinks(node.id);
      links = res.items;
      choice = ownLink(links, node.id) ? 'organisation' : 'restricted';
      error = null;
    } catch (err) {
      error = linkErrorText(err);
    } finally {
      loading = false;
    }
  }

  // linkErrorText says what a refused change means, by its code and reason. The server's English text is
  // never shown.
  function linkErrorText(err: unknown): string {
    if (err instanceof ApiError) {
      const reason = err.details.reason;
      if (err.code === 'forbidden' && reason === 'links_off') return translate($lang, 'share.err.linksOff');
      if (err.code === 'forbidden') return translate($lang, 'share.err.forbidden');
      if (err.code === 'conflict' && reason === 'exists') return translate($lang, 'share.err.exists');
      if (err.code === 'conflict' && reason === 'members_only') return translate($lang, 'share.err.linkMembersOnly');
      if (err.code === 'conflict') return translate($lang, 'share.err.membersOnly');
      if (err.code === 'not_found') return translate($lang, 'share.err.gone');
      if (err.code === 'invalid_request' && err.details.max_expiry_seconds !== undefined) {
        return translatePlural($lang, 'share.err.maxExpiry', maxDays);
      }
      if (err.code === 'invalid_request') return translate($lang, 'share.err.invalid');
      if (err.code === 'rate_limited') return translate($lang, 'share.err.limited');
      if (err.status === 0) return translate($lang, 'error.network');
    }
    return translate($lang, 'error.generic');
  }

  // run performs one change, and shows its refusal. Only one change runs at a time.
  async function run(change: () => Promise<void>): Promise<void> {
    busy = true;
    error = null;
    notice = null;
    try {
      await change();
      onchanged();
    } catch (err) {
      error = linkErrorText(err);
    } finally {
      busy = false;
    }
  }

  // choose switches between Restricted and the account's link. Restricted removes the link, at once; the
  // organisation choice shows the form that makes it.
  async function choose(next: 'restricted' | 'organisation'): Promise<void> {
    if (busy || !canChange) return;
    if (next === 'organisation') {
      choice = 'organisation';
      return;
    }
    const existing = own;
    if (!existing) {
      choice = 'restricted';
      return;
    }
    await run(async () => {
      await deleteShareLink(existing.id);
      links = links.filter((l) => l.id !== existing.id);
      choice = 'restricted';
      madeURL = null;
      notice = translate($lang, 'share.general.removed');
    });
  }

  async function create(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const expires = newExpires === '' ? null : expiryFromInput(newExpires);
    if (expires === null && newExpires !== '') {
      error = translate($lang, 'share.err.invalid');
      return;
    }
    await run(async () => {
      const made = await createShareLink(node.id, {
        role: newRole,
        ...(expires ? { expires_at: expires } : {}),
      });
      links = [...links, made.link];
      madeURL = made.url;
      notice = translate($lang, 'share.general.created');
    });
  }

  async function changeRole(role: Role): Promise<void> {
    const existing = own;
    if (!existing) return;
    await run(async () => {
      const changed = await updateShareLink(existing.id, { role });
      links = links.map((l) => (l.id === changed.id ? { ...l, role: changed.role } : l));
      notice = translate($lang, 'share.general.saved');
    });
  }

  async function changeExpires(value: string): Promise<void> {
    const existing = own;
    if (!existing) return;
    const expires = value === '' ? null : expiryFromInput(value);
    if (value !== '' && expires === null) {
      error = translate($lang, 'share.err.invalid');
      return;
    }
    await run(async () => {
      const changed = await updateShareLink(existing.id, { expires_at: expires });
      links = links.map((l) => (l.id === changed.id ? { ...l, expires_at: changed.expires_at } : l));
      notice = translate($lang, 'share.general.saved');
    });
  }

  async function renew(): Promise<void> {
    const existing = own;
    if (!existing) return;
    await run(async () => {
      const made = await resetShareLink(existing.id);
      links = links.map((l) => (l.id === made.link.id ? made.link : l));
      madeURL = made.url;
      notice = translate($lang, 'share.general.renewed');
    });
  }

  async function copy(): Promise<void> {
    if (madeURL === null) return;
    try {
      await navigator.clipboard.writeText(madeURL);
      notice = translate($lang, 'share.general.copied');
    } catch {
      notice = translate($lang, 'share.general.copyFailed');
    }
  }

  function openNode(nodeID: string): void {
    go(`/n/${nodeID}`);
    onopen();
  }
</script>

<section aria-labelledby="share-general" class="general">
  <h3 id="share-general" class="section-title">{translate($lang, 'share.general')}</h3>
  {#if error}
    <p class="alert error" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="hint" role="status">{notice}</p>
  {/if}
  {#if loading}
    <p class="muted" role="status">{translate($lang, 'share.loading')}</p>
  {:else}
    <fieldset class="choices" disabled={busy || !canChange}>
      <legend class="sr-only">{translate($lang, 'share.general')}</legend>
      <label class="choice">
        <input
          type="radio"
          name="share-general-access"
          value="restricted"
          checked={choice === 'restricted'}
          onchange={() => choose('restricted')}
        />
        <span>{translate($lang, 'share.general.restricted')}</span>
      </label>
      <label class="choice">
        <input
          type="radio"
          name="share-general-access"
          value="organisation"
          checked={choice === 'organisation'}
          onchange={() => choose('organisation')}
        />
        <span>{translate($lang, 'share.general.organisation', { account })}</span>
      </label>
    </fieldset>

    {#if own}
      <div class="own">
        {#if own.paused}
          <p class="pause">
            <strong>{translate($lang, 'share.general.paused')}</strong>
            <span class="hint">{translate($lang, 'share.general.pausedHint')}</span>
          </p>
        {/if}
        <div class="pair">
          <div class="field">
            <label for="share-general-role">{translate($lang, 'share.general.role')}</label>
            <select
              id="share-general-role"
              value={own.role}
              disabled={busy || !canChange}
              onchange={(e) => changeRole((e.currentTarget as HTMLSelectElement).value as Role)}
            >
              {#each roles as r (r)}
                <option value={r}>{translate($lang, `role.${r}`)}</option>
              {/each}
            </select>
          </div>
          <div class="field">
            <label for="share-general-expires">{translate($lang, 'share.general.expires')}</label>
            <input
              id="share-general-expires"
              type="date"
              value={own.expires_at ? dateInputValue(new Date(own.expires_at)) : ''}
              min={earliestDate}
              max={latestDate}
              disabled={busy || !canChange}
              onchange={(e) => changeExpires((e.currentTarget as HTMLInputElement).value)}
            />
          </div>
        </div>
        {#if canChange}
          <div class="foot">
            <button type="button" class="btn secondary" disabled={busy} onclick={renew}>
              {translate($lang, 'share.general.new')}
            </button>
          </div>
        {/if}
      </div>
    {:else if choice === 'organisation' && canChange}
      <form class="pair-form" onsubmit={create}>
        <div class="pair">
          <div class="field">
            <label for="share-general-new-role">{translate($lang, 'share.general.role')}</label>
            <select id="share-general-new-role" bind:value={newRole} disabled={busy}>
              {#each roles as r (r)}
                <option value={r}>{translate($lang, `role.${r}`)}</option>
              {/each}
            </select>
          </div>
          <div class="field">
            <label for="share-general-new-expires">{translate($lang, 'share.general.expires')}</label>
            <input
              id="share-general-new-expires"
              type="date"
              bind:value={newExpires}
              min={earliestDate}
              max={latestDate}
              disabled={busy}
            />
          </div>
        </div>
        <div class="foot">
          <button type="submit" class="btn primary" disabled={busy}>{translate($lang, 'share.general.create')}</button>
        </div>
      </form>
    {/if}

    {#if madeURL !== null}
      <div class="made">
        <p class="hint">{translate($lang, 'share.general.copyNow')}</p>
        <div class="copy-row">
          <input
            type="text"
            readonly
            value={madeURL}
            aria-label={translate($lang, 'share.general.urlLabel')}
            onfocus={(e) => (e.currentTarget as HTMLInputElement).select()}
          />
          <button type="button" class="btn primary" onclick={copy}>{translate($lang, 'share.general.copy')}</button>
        </div>
      </div>
    {/if}

    {#each above as l (l.id)}
      <div class="inherited">
        <span>{translate($lang, 'share.general.inherited', { role: translate($lang, `role.${l.role}`) })}</span>
        <button type="button" class="btn secondary" onclick={() => openNode(l.node_id)}>
          {translate($lang, 'share.openParent')}
        </button>
      </div>
    {/each}
  {/if}
</section>

<style>
  .general {
    display: grid;
    gap: var(--space-3);
  }

  .section-title {
    margin: 0;
    font-size: var(--text-sm, 0.95rem);
  }

  .choices {
    display: grid;
    gap: var(--space-2);
    border: 0;
    margin: 0;
    padding: 0;
  }

  .choice {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .pair {
    display: grid;
    gap: var(--space-3);
    grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }

  .copy-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .copy-row input {
    flex: 1 1 16rem;
    min-width: 0;
  }

  .inherited {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .pause {
    margin: 0;
    display: grid;
    gap: var(--space-1);
  }
</style>
