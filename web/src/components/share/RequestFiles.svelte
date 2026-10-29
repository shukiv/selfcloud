<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import { passwordMinLength } from '../../lib/brand';
  import type { Node, Role } from '../../lib/files/types';
  import { createDropLink, deleteShareLink, listShareLinks, resetShareLink, updateDropLink } from '../../lib/sharing/api';
  import { ownDrop, type ShareLink } from '../../lib/sharing/links';
  import { dateInputValue, maxExpiryDays } from '../../lib/sharing/limits';
  import {
    dropCreateBody,
    dropDateLimits,
    dropDefaultDate,
    dropFormOf,
    dropPatchBody,
    dropProblem,
    dropState,
    type DropForm,
    type DropProblem,
  } from '../../lib/sharing/drop';

  // RequestFiles is the Request files part of the share dialog, for a directory. A drop link lets anyone send
  // files into the directory and see nothing else. The page only shows it where the account allows links that
  // anyone can open and the caller manages the directory (the parent checks this with canRequestFiles). The
  // server checks every change again.
  let {
    node,
    cap,
    onchanged,
  }: {
    node: Node;
    cap: Role | null;
    onchanged: () => void;
  } = $props();

  let drop = $state<ShareLink | null>(null);
  let loading = $state(true);
  let busy = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  // madeURL is the URL of the drop link made or reset just now. It is shown once.
  let madeURL = $state<string | null>(null);
  let form = $state<DropForm>(blankForm());

  let maxDays = $derived($maxExpiryDays);
  let limits = $derived(dropDateLimits(new Date(), maxDays));
  let minPassword = $derived($passwordMinLength);
  let manager = $derived(cap === 'manager');

  onMount(() => {
    void load();
  });

  function blankForm(): DropForm {
    return {
      date: dropDefaultDate(new Date(), $maxExpiryDays),
      password: '',
      passwordChanged: false,
      title: '',
      message: '',
      askName: false,
      notify: false,
    };
  }

  function formOf(l: ShareLink): DropForm {
    return dropFormOf(l, (iso) => dateInputValue(new Date(iso)));
  }

  async function load(): Promise<void> {
    loading = true;
    try {
      const res = await listShareLinks(node.id);
      drop = ownDrop(res.items, node.id);
      if (drop) form = formOf(drop);
      error = null;
    } catch (err) {
      error = dropErrorText(err);
    } finally {
      loading = false;
    }
  }

  // dropErrorText says what a refused change means, by its code and reason. The server's English text is never shown.
  function dropErrorText(err: unknown): string {
    if (err instanceof ApiError) {
      const reason = err.details.reason;
      if (err.code === 'forbidden' && reason === 'public_links_off') return translate($lang, 'share.err.publicOff');
      if (err.code === 'forbidden' && reason === 'links_off') return translate($lang, 'share.err.linksOff');
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

  // problemText says what the form needs changed, before anything is sent.
  function problemText(p: DropProblem): string {
    switch (p) {
      case 'date':
        return translate($lang, 'drop.err.date');
      case 'title_long':
        return translate($lang, 'drop.err.title');
      case 'message_long':
        return translate($lang, 'drop.err.message');
      case 'password_short':
        return translate($lang, 'share.err.passwordShort', { count: minPassword });
      case 'password_long':
        return translate($lang, 'share.err.passwordLong');
    }
  }

  // run performs one change and shows its refusal. Only one change runs at a time.
  async function run(change: () => Promise<void>): Promise<void> {
    busy = true;
    error = null;
    notice = null;
    try {
      await change();
      onchanged();
    } catch (err) {
      error = dropErrorText(err);
    } finally {
      busy = false;
    }
  }

  // turnOn makes the drop link with the form's values.
  async function turnOn(): Promise<void> {
    const problem = dropProblem(form, new Date(), maxDays, minPassword);
    if (problem !== null) {
      error = problemText(problem);
      return;
    }
    const body = dropCreateBody(form, new Date(), maxDays, minPassword);
    if (body === null) {
      error = translate($lang, 'drop.err.date');
      return;
    }
    await run(async () => {
      const made = await createDropLink(node.id, body);
      drop = made.link;
      form = formOf(made.link);
      madeURL = made.url;
      notice = translate($lang, 'drop.notice.on');
    });
  }

  // turnOff removes the drop link at once. Its URL stops working, and files already sent are kept.
  async function turnOff(): Promise<void> {
    const existing = drop;
    if (!existing) return;
    await run(async () => {
      await deleteShareLink(existing.id);
      drop = null;
      madeURL = null;
      form = blankForm();
      notice = translate($lang, 'drop.notice.off');
    });
  }

  // save changes the drop link's end, text, switches and password.
  async function save(): Promise<void> {
    const existing = drop;
    if (!existing) return;
    const problem = dropProblem(form, new Date(), maxDays, minPassword);
    if (problem !== null) {
      error = problemText(problem);
      return;
    }
    const body = dropPatchBody(form, new Date(), maxDays, minPassword);
    if (body === null) {
      error = translate($lang, 'drop.err.date');
      return;
    }
    await run(async () => {
      const changed = await updateDropLink(existing.id, body);
      drop = changed;
      form = formOf(changed);
      notice = translate($lang, 'share.general.saved');
    });
  }

  // renew gives the drop link a new secret. The old URL stops working at once.
  async function renew(): Promise<void> {
    const existing = drop;
    if (!existing) return;
    await run(async () => {
      const made = await resetShareLink(existing.id);
      drop = made.link;
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

  function onToggle(e: Event): void {
    if (busy || !manager) return;
    const on = (e.currentTarget as HTMLInputElement).checked;
    if (on) void turnOn();
    else void turnOff();
  }
</script>

<section aria-labelledby="share-request-files" class="request">
  <h3 id="share-request-files" class="section-title">{translate($lang, 'drop.title')}</h3>
  <p class="hint">{translate($lang, 'drop.hint')}</p>

  {#if error}
    <p class="alert error" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="hint" role="status">{notice}</p>
  {/if}

  {#if loading}
    <p class="muted" role="status">{translate($lang, 'share.loading')}</p>
  {:else}
    <label class="toggle">
      <input
        type="checkbox"
        checked={drop !== null}
        disabled={busy || !manager}
        onchange={onToggle}
      />
      <span>{translate($lang, 'drop.toggle')}</span>
    </label>

    {#if drop !== null}
      {#if dropState(drop) === 'paused'}
        <p class="pause">
          <strong>{translate($lang, 'share.general.paused')}</strong>
          <span class="hint">{translate($lang, 'share.general.pausedHint')}</span>
        </p>
      {/if}

      <form
        class="stack"
        onsubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div class="pair">
          <div class="field">
            <label for="drop-expires">{translate($lang, 'drop.ends')}</label>
            <input
              id="drop-expires"
              type="date"
              bind:value={form.date}
              min={limits.earliest}
              max={limits.latest}
              required
              disabled={busy || !manager}
            />
          </div>
        </div>
        <div class="field">
          <label for="drop-title">{translate($lang, 'drop.titleField')}</label>
          <input id="drop-title" type="text" bind:value={form.title} maxlength="400" disabled={busy || !manager} />
          <p class="hint">{translate($lang, 'drop.titleHint')}</p>
        </div>
        <div class="field">
          <label for="drop-message">{translate($lang, 'drop.message')}</label>
          <textarea id="drop-message" rows="3" bind:value={form.message} disabled={busy || !manager}></textarea>
        </div>
        <label class="toggle">
          <input type="checkbox" bind:checked={form.askName} disabled={busy || !manager} />
          <span>{translate($lang, 'drop.askName')}</span>
        </label>
        <label class="toggle">
          <input type="checkbox" bind:checked={form.notify} disabled={busy || !manager} />
          <span>{translate($lang, 'drop.notify')}</span>
        </label>
        <div class="field">
          <p class="hint">
            {drop.password_set ? translate($lang, 'share.general.passwordSet') : translate($lang, 'share.general.noPassword')}
          </p>
          <label for="drop-password">
            {translate($lang, drop.password_set ? 'share.general.passwordChange' : 'share.general.passwordAdd')}
          </label>
          <input
            id="drop-password"
            type="password"
            autocomplete="new-password"
            disabled={busy || !manager}
            bind:value={form.password}
            oninput={() => (form.passwordChanged = true)}
          />
          <p class="hint">{translate($lang, 'share.general.passwordHint', { count: minPassword })}</p>
        </div>
        {#if manager}
          <div class="foot">
            <button type="button" class="btn secondary" disabled={busy} onclick={renew}>
              {translate($lang, 'share.general.new')}
            </button>
            <button type="submit" class="btn primary" disabled={busy}>{translate($lang, 'drop.save')}</button>
          </div>
        {/if}
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
  {/if}
</section>

<style>
  .request {
    display: grid;
    gap: var(--space-3);
  }

  .section-title {
    margin: 0;
    font-size: var(--text-sm, 0.95rem);
  }

  .toggle {
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

  .pause {
    margin: 0;
    display: grid;
    gap: var(--space-1);
  }
</style>
