<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import { session } from '../../lib/session';
  import { atLeast } from '../../lib/files/roles';
  import { formatDate } from '../../lib/files/format';
  import { statOrNull } from '../../lib/files/api';
  import { go } from '../../lib/router';
  import type { DirectoryEntry, FolderAccess, Node, Permission, Role } from '../../lib/files/types';
  import {
    listPermissions,
    removePermission,
    setPermission,
    transferOwnership,
    updateNodeFlags,
  } from '../../lib/sharing/api';
  import { grantableRoles, shareCap } from '../../lib/sharing/roles';
  import { ancestorIDs, canRemoveHere, originOf, type Origin } from '../../lib/sharing/inherited';
  import { dateInputValue, expiryFromInput, expiryLimit, maxExpiryDays } from '../../lib/sharing/limits';
  import Dialog from '../Dialog.svelte';
  import PeopleCombobox from './PeopleCombobox.svelte';
  import GeneralAccess from './GeneralAccess.svelte';
  import RequestFiles from './RequestFiles.svelte';
  import { canRequestFiles } from '../../lib/sharing/drop';

  // ShareDialog shows who has access to an item, and changes it as far as the caller may. The server checks every
  // change; the dialog only shows what the caller's role allows. A person below manager sees only its own rows
  // (the server's rule), and says so.
  let {
    node,
    space,
    onclose,
    onchanged,
  }: {
    node: Node;
    space: FolderAccess | null;
    onclose: () => void;
    onchanged: () => void;
  } = $props();

  let perms = $state<Permission[]>([]);
  let names = $state<Record<string, string>>({});
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let formError = $state<string | null>(null);
  let busy = $state(false);

  // The add form.
  let picked = $state<DirectoryEntry | null>(null);
  let newRole = $state<Role>('viewer');
  let expires = $state('');
  let notify = $state(true);
  let message = $state('');
  let pickedInvalid = $state(false);

  // A removal waits for a confirmation.
  let confirmRemove = $state<Permission | null>(null);

  // The advanced settings, read from the node and kept here while the dialog is open.
  let editorsCanShare = $state(false);
  let noDownload = $state(false);
  $effect(() => {
    editorsCanShare = node.editors_can_share;
    noDownload = node.no_download;
  });

  // Ownership moves to another user.
  let transferOpen = $state(false);
  let transferTo = $state<DirectoryEntry | null>(null);
  let transferConfirm = $state(false);

  let manager = $derived(atLeast(node.role, 'manager'));
  let cap = $derived(
    shareCap({
      role: node.role,
      editorsCanShare: node.editors_can_share,
      managersOnly: space?.sharing_managers_only ?? false,
    }),
  );
  let roleOptions = $derived(grantableRoles(cap));
  let canShareHere = $derived(roleOptions.length > 0);
  let personal = $derived(space?.kind === 'personal');
  let isOwner = $derived(node.owner_id !== undefined && node.owner_id === $session?.user.id);
  let maxDays = $derived($maxExpiryDays);
  let latest = $derived(expiryLimit(maxDays, new Date()));
  let earliestDate = $derived(dateInputValue(new Date(Date.now() + 24 * 60 * 60 * 1000)));
  let latestDate = $derived(latest ? dateInputValue(latest) : undefined);

  onMount(() => {
    void load();
  });

  async function load(): Promise<void> {
    loading = true;
    try {
      const res = await listPermissions(node.id);
      perms = res.items;
      loadError = null;
      const missing = ancestorIDs(res.items).filter((id) => names[id] === undefined);
      for (const id of missing) {
        const parent = await statOrNull(id);
        if (parent) names = { ...names, [id]: parent.name };
      }
    } catch (err) {
      loadError = shareErrorText(err);
    } finally {
      loading = false;
    }
  }

  function changed(text: string | null) {
    notice = text;
    onchanged();
  }

  // shareErrorText says what a refused change means, by its code. The server's English text is never shown.
  function shareErrorText(err: unknown): string {
    if (err instanceof ApiError) {
      if (err.code === 'forbidden') return translate($lang, 'share.err.forbidden');
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

  // noticeFor says why no mail went out, when the server says so. A share that was made is never undone for it.
  function noticeFor(reason: string | undefined): string | null {
    switch (reason) {
      case undefined:
      case 'not_requested':
        return null;
      case 'too_many_recipients':
        return translate($lang, 'share.notice.tooMany');
      case 'no_recipients':
        return translate($lang, 'share.notice.noRecipients');
      case 'recipient_not_active':
        return translate($lang, 'share.notice.notActive');
      case 'mail_budget':
        return translate($lang, 'share.notice.budget');
      case 'mail_failed':
        return translate($lang, 'share.notice.failed');
      case 'mail_not_configured':
        return translate($lang, 'share.notice.notConfigured');
      default:
        return translate($lang, 'share.notice.none');
    }
  }

  // subjectName names a row for the people who see it. A name comes only to a manager; the caller's own row is "You".
  function subjectName(p: Permission): string {
    if (p.subject_name) return p.subject_name;
    if (p.subject_kind === 'user' && p.subject_id === $session?.user.id) return translate($lang, 'share.you');
    return p.subject_kind === 'group' ? translate($lang, 'share.subject.group') : translate($lang, 'share.subject.person');
  }

  function originText(p: Permission): string {
    const o: Origin = originOf(p, new Map(Object.entries(names)));
    if (o.kind === 'own') return '';
    if (o.kind === 'named') return translate($lang, 'share.from', { name: o.name });
    return translate($lang, 'share.fromParent');
  }

  async function changeRole(p: Permission, role: Role): Promise<void> {
    if (role === p.role) return;
    formError = null;
    busy = true;
    try {
      // A change of role is not mailed again; only a new share is.
      await setPermission(node.id, p.subject_id, { role, notify: false });
      changed(translate($lang, 'share.changed', { name: subjectName(p), role: translate($lang, `role.${role}`) }));
      await load();
    } catch (err) {
      formError = shareErrorText(err);
    } finally {
      busy = false;
    }
  }

  async function remove(p: Permission): Promise<void> {
    confirmRemove = null;
    busy = true;
    try {
      await removePermission(node.id, p.subject_id);
      changed(translate($lang, 'share.removed', { name: subjectName(p) }));
      await load();
    } catch (err) {
      formError = shareErrorText(err);
    } finally {
      busy = false;
    }
  }

  async function add(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    formError = null;
    notice = null;
    if (picked === null) {
      pickedInvalid = true;
      formError = translate($lang, 'share.err.pick');
      return;
    }
    pickedInvalid = false;
    const expiresAt = expiryFromInput(expires);
    if (expires !== '' && expiresAt === null) {
      formError = translate($lang, 'share.err.date');
      return;
    }
    if (message.length > 500) {
      formError = translate($lang, 'share.err.message');
      return;
    }
    const who = picked.name;
    busy = true;
    try {
      const reply = await setPermission(node.id, picked.id, {
        role: newRole,
        notify,
        ...(expiresAt ? { expires_at: expiresAt } : {}),
        ...(message.trim() ? { message: message.trim() } : {}),
      });
      const mailNote = notify && reply.notified === false ? noticeFor(reply.reason) : null;
      const done = translate($lang, 'share.done', { name: who, role: translate($lang, `role.${newRole}`) });
      changed(mailNote ? `${done} ${mailNote}` : done);
      picked = null;
      expires = '';
      message = '';
      notify = true;
      await load();
    } catch (err) {
      formError = shareErrorText(err);
    } finally {
      busy = false;
    }
  }

  async function setEditorsCanShare(on: boolean): Promise<void> {
    formError = null;
    busy = true;
    try {
      await updateNodeFlags(node.id, { editors_can_share: on });
      editorsCanShare = on;
      changed(translate($lang, on ? 'share.flag.editorsOn' : 'share.flag.editorsOff'));
    } catch (err) {
      editorsCanShare = !on;
      formError = shareErrorText(err);
    } finally {
      busy = false;
    }
  }

  async function setNoDownload(on: boolean): Promise<void> {
    formError = null;
    busy = true;
    try {
      await updateNodeFlags(node.id, { no_download: on });
      noDownload = on;
      changed(translate($lang, on ? 'share.flag.noDownloadOn' : 'share.flag.noDownloadOff'));
    } catch (err) {
      noDownload = !on;
      formError = shareErrorText(err);
    } finally {
      busy = false;
    }
  }

  async function transfer(): Promise<void> {
    if (transferTo === null) return;
    const who = transferTo.name;
    busy = true;
    formError = null;
    try {
      await transferOwnership(node.id, transferTo.id);
      transferOpen = false;
      transferConfirm = false;
      changed(translate($lang, 'share.transferred', { name: who }));
      onchanged();
    } catch (err) {
      formError = shareErrorText(err);
    } finally {
      busy = false;
    }
  }

  function openParent(p: Permission) {
    if (p.inherited_from) {
      go(`/n/${p.inherited_from}`);
      onclose();
    }
  }

  let title = $derived(translate($lang, 'share.title', { name: node.name }));
</script>

<Dialog {title} id="share" wide onclose={onclose}>
  <div class="share">
    <p class="live" role="status" aria-live="polite">{notice ?? ''}</p>
    {#if formError}
      <p class="alert error" role="alert">{formError}</p>
    {/if}

    {#if !manager}
      <p class="own-access">{translate($lang, 'share.yourAccess', { role: translate($lang, `role.${node.role ?? 'viewer'}`) })}</p>
    {/if}

    <GeneralAccess {node} {cap} onchanged={onchanged} onopen={onclose} />

    {#if node.kind === 'dir' && canRequestFiles($session?.publicLinks === true, cap)}
      <RequestFiles {node} {cap} onchanged={onchanged} />
    {/if}

    <section aria-labelledby="share-people">
      <h3 id="share-people" class="section-title">{manager ? translate($lang, 'share.people') : translate($lang, 'share.yourRows')}</h3>
      {#if loading}
        <p class="muted" role="status">{translate($lang, 'share.loading')}</p>
      {:else if loadError}
        <p class="alert error" role="alert">{loadError}</p>
      {:else if perms.length === 0}
        <p class="muted">{translate($lang, 'share.none')}</p>
      {:else}
        <ul class="rows">
          {#each perms as p (p.subject_kind + p.subject_id + (p.inherited_from ?? ''))}
            {@const who = subjectName(p)}
            <li class="row">
              <div class="who">
                <span class="name"><bdi>{who}</bdi></span>
                {#if p.subject_status === 'pending'}
                  <span class="badge warn">{translate($lang, 'share.badge.invited')}</span>
                {/if}
                {#if p.subject_kind === 'group'}
                  <span class="badge">{translate($lang, 'share.kind.group')}</span>
                {/if}
                {#if originText(p)}
                  <span class="origin">{originText(p)}</span>
                {/if}
                {#if p.expires_at}
                  <span class="origin">{translate($lang, 'share.endsOn', { date: formatDate(p.expires_at, $lang) })}</span>
                {/if}
              </div>
              <div class="controls">
                {#if manager && canRemoveHere(p) && !(p.subject_id === $session?.user.id && p.role === 'manager')}
                  <label class="sr-only" for={`role-${p.subject_id}`}>{translate($lang, 'share.roleFor', { name: who })}</label>
                  <select
                    id={`role-${p.subject_id}`}
                    value={p.role}
                    disabled={busy}
                    onchange={(e) => changeRole(p, (e.currentTarget as HTMLSelectElement).value as Role)}
                  >
                    {#each roleOptions as r (r)}
                      <option value={r}>{translate($lang, `role.${r}`)}</option>
                    {/each}
                    {#if !roleOptions.includes(p.role)}
                      <option value={p.role} disabled>{translate($lang, `role.${p.role}`)}</option>
                    {/if}
                  </select>
                  {#if confirmRemove?.subject_id === p.subject_id}
                    <span class="confirm">
                      <span>{translate($lang, 'share.removeAsk', { name: who })}</span>
                      <button type="button" class="btn danger-fill" onclick={() => remove(p)} disabled={busy}>{translate($lang, 'share.removeYes')}</button>
                      <button type="button" class="btn secondary" onclick={() => (confirmRemove = null)}>{translate($lang, 'common.cancel')}</button>
                    </span>
                  {:else}
                    <button type="button" class="btn secondary" onclick={() => (confirmRemove = p)} disabled={busy}>{translate($lang, 'share.remove')}</button>
                  {/if}
                {:else if manager}
                  <span class="badge">{translate($lang, `role.${p.role}`)}</span>
                  {#if p.inherited_from}
                    <button type="button" class="btn secondary" onclick={() => openParent(p)}>{translate($lang, 'share.openParent')}</button>
                  {/if}
                {:else}
                  <span class="badge">{translate($lang, `role.${p.role}`)}</span>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    {#if canShareHere}
      <section aria-labelledby="share-add" class="add">
        <h3 id="share-add" class="section-title">{translate($lang, 'share.add')}</h3>
        <form onsubmit={add} class="form">
          <PeopleCombobox
            id="share-who"
            label={translate($lang, 'share.who')}
            hint={translate($lang, 'share.whoHint')}
            invalid={pickedInvalid}
            errorId={pickedInvalid ? 'share-who-error' : undefined}
            onpick={(entry) => {
              picked = entry;
              pickedInvalid = false;
            }}
          />
          {#if pickedInvalid}
            <p class="field-error" id="share-who-error">{translate($lang, 'share.err.pick')}</p>
          {/if}
          <div class="pair">
            <div class="field">
              <label for="share-role">{translate($lang, 'share.role')}</label>
              <select id="share-role" bind:value={newRole}>
                {#each roleOptions as r (r)}
                  <option value={r}>{translate($lang, `role.${r}`)}</option>
                {/each}
              </select>
            </div>
            <div class="field">
              <label for="share-expires">{translate($lang, 'share.expires')}</label>
              <input
                id="share-expires"
                type="date"
                bind:value={expires}
                min={earliestDate}
                max={latestDate}
                aria-describedby="share-expires-hint"
              />
              <p class="hint" id="share-expires-hint">
                {maxDays > 0
                  ? translatePlural($lang, 'share.expiresHintCap', maxDays)
                  : translate($lang, 'share.expiresHint')}
              </p>
            </div>
          </div>
          <div class="field">
            <label for="share-message">{translate($lang, 'share.message')}</label>
            <textarea id="share-message" rows="2" maxlength="500" bind:value={message} aria-describedby="share-message-hint"></textarea>
            <p class="hint" id="share-message-hint">{translate($lang, 'share.messageHint', { count: message.length })}</p>
          </div>
          <label class="check">
            <input id="share-notify" type="checkbox" bind:checked={notify} />
            <span>{translate($lang, 'share.notify')}</span>
          </label>
          <div class="foot">
            <button type="submit" class="btn primary" disabled={busy}>{translate($lang, 'share.submit')}</button>
          </div>
        </form>
      </section>
    {:else if !manager}
      <p class="muted">{translate($lang, 'share.cannotShare')}</p>
    {/if}

    {#if manager}
      <section aria-labelledby="share-advanced" class="advanced">
        <h3 id="share-advanced" class="section-title">{translate($lang, 'share.advanced')}</h3>
        <label class="check">
          <input
            type="checkbox"
            checked={editorsCanShare}
            disabled={busy}
            onchange={(e) => setEditorsCanShare((e.currentTarget as HTMLInputElement).checked)}
          />
          <span>{translate($lang, 'share.editorsCanShare')}</span>
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={noDownload}
            disabled={busy}
            aria-describedby="share-nodl-hint"
            onchange={(e) => setNoDownload((e.currentTarget as HTMLInputElement).checked)}
          />
          <span>{translate($lang, 'share.noDownload')}</span>
        </label>
        <p class="hint" id="share-nodl-hint">{translate($lang, 'share.noDownloadHint')}</p>

        {#if isOwner}
          <div class="transfer">
            {#if !transferOpen}
              <button type="button" class="btn secondary" onclick={() => (transferOpen = true)}>{translate($lang, 'share.transfer')}</button>
            {:else}
              <PeopleCombobox
                id="share-transfer-to"
                label={translate($lang, 'share.transferTo')}
                hint={translate($lang, 'share.transferHint')}
                onpick={(entry) => {
                  transferTo = entry?.kind === 'user' ? entry : null;
                  transferConfirm = false;
                }}
              />
              {#if transferTo}
                <p class="hint">{translate($lang, personal ? 'share.transferPersonal' : 'share.transferTeam', { name: transferTo.name })}</p>
                {#if !transferConfirm}
                  <button type="button" class="btn danger" onclick={() => (transferConfirm = true)}>{translate($lang, 'share.transferGo')}</button>
                {:else}
                  <p class="alert error" role="alert">{translate($lang, 'share.transferSure', { name: transferTo.name })}</p>
                  <button type="button" class="btn danger-fill" onclick={transfer} disabled={busy}>{translate($lang, 'share.transferYes')}</button>
                {/if}
              {/if}
            {/if}
          </div>
        {/if}
      </section>
    {/if}
  </div>
</Dialog>

<style>
  .share {
    display: grid;
    gap: var(--space-6);
    min-width: 0;
  }

  .live:empty {
    display: none;
  }

  .section-title {
    margin: 0 0 var(--space-2);
    font-size: var(--text-base);
    font-weight: 600;
  }

  .own-access,
  .muted {
    margin: 0;
    color: var(--color-muted-foreground);
  }

  .rows {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    min-height: 44px;
    padding-block: var(--space-2);
    border-block-end: 1px solid var(--color-border);
  }

  .who {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
  }

  .name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .origin {
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }

  .confirm {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }

  .badge {
    display: inline-block;
    padding: 2px var(--space-2);
    font-size: var(--text-xs);
    font-weight: 500;
    color: var(--color-foreground);
    background: var(--color-muted);
    border-radius: var(--radius-full);
  }

  .badge.warn {
    color: var(--color-warning-fg);
    background: var(--color-warning-bg);
  }

  .form,
  .advanced,
  .transfer {
    display: grid;
    gap: var(--space-3);
  }

  .field {
    display: grid;
    gap: var(--space-2);
    min-width: 0;
  }

  .pair {
    display: grid;
    gap: var(--space-3);
  }

  @media (width >= 560px) {
    .pair {
      grid-template-columns: 1fr 1fr;
    }
  }

  .hint {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .field-error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  input[type='date'],
  select,
  textarea {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  textarea {
    padding-block: var(--space-2);
  }

  .check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
  }

  .check input {
    width: 20px;
    height: 20px;
  }

  .foot {
    display: flex;
    justify-content: flex-end;
  }

  .danger-fill {
    background: var(--color-danger);
    color: var(--color-on-danger);
    border-color: var(--color-danger);
  }

  .danger {
    color: var(--color-danger);
  }

  .advanced {
    padding-block-start: var(--space-4);
    border-block-start: 1px solid var(--color-border);
  }
</style>
