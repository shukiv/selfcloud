<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import { session } from '../../lib/session';
  import { passwordMinLength } from '../../lib/brand';
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
  import {
    anyoneExpiresAt,
    defaultAnyoneEnd,
    latestAnyoneEnd,
    passwordProblem,
  } from '../../lib/sharing/anyone';

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
  let choice = $state<'restricted' | 'organisation' | 'anyone'>('restricted');
  let newRole = $state<Role>('viewer');
  let newExpires = $state('');
  // The anyone form: its end date (required, 30 days by default) and an optional password, which is never shown
  // back. editPassword is the new password for a link that already has one.
  let anyoneDate = $state(dateInputValue(defaultAnyoneEnd(new Date(), $maxExpiryDays)));
  let anyonePassword = $state('');
  let editPassword = $state('');

  let own = $derived(ownLink(links, node.id));
  let above = $derived(inheritedLinks(links, node.id));
  let roles = $derived(linkRoleOptions(cap));
  let canChange = $derived(cap !== null);
  let account = $derived($session?.account?.name ?? '');
  let maxDays = $derived($maxExpiryDays);
  let latest = $derived(expiryLimit(maxDays, new Date()));
  let latestDate = $derived(latest ? dateInputValue(latest) : undefined);
  let earliestDate = $derived(dateInputValue(new Date(Date.now() + 24 * 60 * 60 * 1000)));
  // An anyone link is offered only where the account allows it and the caller manages the item. A link that
  // anyone can open gives viewer only, and no role list.
  let canAnyone = $derived($session?.publicLinks === true && cap === 'manager');
  let anyoneLatest = $derived(dateInputValue(latestAnyoneEnd(new Date(), maxDays)));
  let anyoneEarliest = $derived(dateInputValue(new Date(Date.now() + 24 * 60 * 60 * 1000)));
  let minPassword = $derived($passwordMinLength);

  onMount(() => {
    void load();
  });

  async function load(): Promise<void> {
    loading = true;
    try {
      const res = await listShareLinks(node.id);
      links = res.items;
      const mine = ownLink(links, node.id);
      choice = mine ? (mine.audience === 'anyone' ? 'anyone' : 'organisation') : 'restricted';
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
      if (err.code === 'forbidden' && reason === 'public_links_off') return translate($lang, 'share.err.publicOff');
      if (err.code === 'forbidden' && reason === 'manager_required') return translate($lang, 'share.err.anyoneManager');
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
  async function choose(next: 'restricted' | 'organisation' | 'anyone'): Promise<void> {
    if (busy || !canChange) return;
    if (next === 'organisation' || next === 'anyone') {
      choice = next;
      const existing = own;
      // A link that exists changes its audience; it keeps its secret. The server gives a viewer link an end date
      // when it has none, and clears a password when the link becomes an organisation link.
      if (existing && existing.audience !== next) {
        if (next === 'anyone' && !canAnyone) return;
        await run(async () => {
          const changed = await updateShareLink(existing.id, { audience: next });
          links = links.map((l) => (l.id === changed.id ? changed : l));
          notice = translate($lang, 'share.general.saved');
        });
      }
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
    if (choice === 'anyone') return createAnyone();
    const expires = newExpires === '' ? null : expiryFromInput(newExpires);
    if (expires === null && newExpires !== '') {
      error = translate($lang, 'share.err.invalid');
      return;
    }
    await run(async () => {
      const made = await createShareLink(node.id, {
        audience: 'organisation',
        role: newRole,
        ...(expires ? { expires_at: expires } : {}),
      });
      links = [...links, made.link];
      madeURL = made.url;
      notice = translate($lang, 'share.general.created');
    });
  }

  // createAnyone makes the link that anyone can open. The end date is required; the server would take none.
  async function createAnyone(): Promise<void> {
    const at = anyoneExpiresAt(anyoneDate, new Date(), maxDays);
    if (at === null) {
      error = translate($lang, 'share.err.date');
      return;
    }
    const problem = passwordProblem(anyonePassword, minPassword);
    if (problem !== null) {
      error = translate($lang, problem === 'short' ? 'share.err.passwordShort' : 'share.err.passwordLong', {
        count: minPassword,
      });
      return;
    }
    const password = anyonePassword;
    await run(async () => {
      const made = await createShareLink(node.id, {
        audience: 'anyone',
        expires_at: at,
        ...(password !== '' ? { password } : {}),
      });
      anyonePassword = '';
      links = [...links, made.link];
      madeURL = made.url;
      notice = translate($lang, 'share.general.created');
    });
  }

  // changeAnyoneEnds moves the end date of an anyone link. Clearing it is not offered.
  async function changeAnyoneEnds(value: string): Promise<void> {
    const existing = own;
    if (!existing) return;
    const at = anyoneExpiresAt(value, new Date(), maxDays);
    if (at === null) {
      error = translate($lang, 'share.err.date');
      return;
    }
    await run(async () => {
      const changed = await updateShareLink(existing.id, { expires_at: at });
      links = links.map((l) => (l.id === changed.id ? changed : l));
      notice = translate($lang, 'share.general.saved');
    });
  }

  // changeAnyonePassword sets a new password on an anyone link, or removes it when the field is cleared with the
  // remove button. The password is sent once and never kept after the change.
  async function changeAnyonePassword(password: string): Promise<void> {
    const existing = own;
    if (!existing) return;
    const problem = password === '' ? null : passwordProblem(password, minPassword);
    if (problem !== null) {
      error = translate($lang, problem === 'short' ? 'share.err.passwordShort' : 'share.err.passwordLong', {
        count: minPassword,
      });
      return;
    }
    await run(async () => {
      const changed = await updateShareLink(existing.id, { password });
      links = links.map((l) => (l.id === changed.id ? changed : l));
      editPassword = '';
      notice = translate($lang, 'share.general.saved');
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
      {#if canAnyone || own?.audience === 'anyone'}
        <label class="choice">
          <input
            type="radio"
            name="share-general-access"
            value="anyone"
            checked={choice === 'anyone'}
            disabled={!canAnyone && own?.audience !== 'anyone'}
            onchange={() => choose('anyone')}
          />
          <span>{translate($lang, 'share.general.anyone')}</span>
        </label>
      {/if}
    </fieldset>
    {#if choice === 'anyone'}
      <p class="hint">{translate($lang, 'share.general.anyoneHint')}</p>
    {/if}

    {#if own && own.audience === 'anyone'}
      <div class="own">
        {#if own.paused}
          <p class="pause">
            <strong>{translate($lang, 'share.general.paused')}</strong>
            <span class="hint">{translate($lang, 'share.general.pausedHint')}</span>
          </p>
        {/if}
        <p class="hint">{translate($lang, 'share.general.viewerOnly')}</p>
        <div class="pair">
          <div class="field">
            <label for="share-general-expires">{translate($lang, 'share.general.endsRequired')}</label>
            <input
              id="share-general-expires"
              type="date"
              value={own.expires_at ? dateInputValue(new Date(own.expires_at)) : ''}
              min={anyoneEarliest}
              max={anyoneLatest}
              disabled={busy || !canChange}
              onchange={(e) => changeAnyoneEnds((e.currentTarget as HTMLInputElement).value)}
            />
          </div>
        </div>
        <div class="field">
          <p class="hint">
            {own.password_set ? translate($lang, 'share.general.passwordSet') : translate($lang, 'share.general.noPassword')}
          </p>
          <label for="share-general-password-edit">
            {translate($lang, own.password_set ? 'share.general.passwordChange' : 'share.general.passwordAdd')}
          </label>
          <div class="copy-row">
            <input
              id="share-general-password-edit"
              type="password"
              autocomplete="new-password"
              bind:value={editPassword}
              disabled={busy || !canChange}
            />
            <button
              type="button"
              class="btn secondary"
              disabled={busy || !canChange || editPassword === ''}
              onclick={() => changeAnyonePassword(editPassword)}
            >
              {translate($lang, 'share.general.passwordSave')}
            </button>
            {#if own.password_set}
              <button
                type="button"
                class="btn secondary"
                disabled={busy || !canChange}
                onclick={() => changeAnyonePassword('')}
              >
                {translate($lang, 'share.general.passwordRemove')}
              </button>
            {/if}
          </div>
          <p class="hint">{translate($lang, 'share.general.passwordHint', { count: minPassword })}</p>
        </div>
        {#if canChange}
          <div class="foot">
            <button type="button" class="btn secondary" disabled={busy} onclick={renew}>
              {translate($lang, 'share.general.new')}
            </button>
          </div>
        {/if}
      </div>
    {:else if own}
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
    {:else if choice === 'anyone' && canChange}
      <form class="pair-form" onsubmit={create}>
        <p class="hint">{translate($lang, 'share.general.viewerOnly')}</p>
        <div class="pair">
          <div class="field">
            <label for="share-anyone-expires">{translate($lang, 'share.general.endsRequired')}</label>
            <input
              id="share-anyone-expires"
              type="date"
              bind:value={anyoneDate}
              min={anyoneEarliest}
              max={anyoneLatest}
              required
              disabled={busy}
            />
          </div>
          <div class="field">
            <label for="share-anyone-password">{translate($lang, 'share.general.password')}</label>
            <input
              id="share-anyone-password"
              type="password"
              autocomplete="new-password"
              bind:value={anyonePassword}
              disabled={busy}
            />
            <p class="hint">{translate($lang, 'share.general.passwordHint', { count: minPassword })}</p>
          </div>
        </div>
        <div class="foot">
          <button type="submit" class="btn primary" disabled={busy}>{translate($lang, 'share.general.create')}</button>
        </div>
      </form>
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
