<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import type { FolderAccess } from '../../lib/files/types';
  import { updateSpaceSettings, type SpaceSettings } from '../../lib/sharing/api';
  import Dialog from '../Dialog.svelte';

  // SpaceSettings holds a team space's restrictions. Only a manager of the space's root changes them, and the
  // server checks each change. Each switch saves when it changes.
  let {
    space,
    onclose,
    onsaved,
  }: { space: FolderAccess; onclose: () => void; onsaved: (f: FolderAccess) => void } = $props();

  let values = $state<Required<SpaceSettings>>({
    members_only: false,
    sharing_managers_only: false,
    no_download_for_viewers: false,
    allow_links: false,
  });
  let status = $state<string | null>(null);
  let error = $state<string | null>(null);
  let busy = $state(false);

  $effect(() => {
    values = {
      members_only: space.members_only,
      sharing_managers_only: space.sharing_managers_only,
      no_download_for_viewers: space.no_download_for_viewers,
      allow_links: space.allow_links,
    };
  });

  async function save(key: keyof SpaceSettings, on: boolean) {
    const before = values[key];
    values = { ...values, [key]: on };
    busy = true;
    error = null;
    status = null;
    try {
      await updateSpaceSettings(space.id, { [key]: on });
      status = translate($lang, 'space.saved');
      onsaved({ ...space, ...values });
    } catch (err) {
      values = { ...values, [key]: before };
      error = err instanceof ApiError && err.code === 'forbidden'
        ? translate($lang, 'space.err.forbidden')
        : translate($lang, 'error.generic');
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={translate($lang, 'space.title', { name: space.name })} id="space" wide onclose={onclose}>
  <div class="settings">
    <p class="live" role="status" aria-live="polite">{status ?? ''}</p>
    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <label class="switch">
      <input type="checkbox" checked={values.members_only} disabled={busy} onchange={(e) => save('members_only', (e.currentTarget as HTMLInputElement).checked)} />
      <span>
        <span class="label">{translate($lang, 'space.membersOnly')}</span>
        <span class="hint">{translate($lang, 'space.membersOnlyHint')}</span>
      </span>
    </label>
    <label class="switch">
      <input type="checkbox" checked={values.sharing_managers_only} disabled={busy} onchange={(e) => save('sharing_managers_only', (e.currentTarget as HTMLInputElement).checked)} />
      <span>
        <span class="label">{translate($lang, 'space.managersOnly')}</span>
        <span class="hint">{translate($lang, 'space.managersOnlyHint')}</span>
      </span>
    </label>
    <label class="switch">
      <input type="checkbox" checked={values.no_download_for_viewers} disabled={busy} onchange={(e) => save('no_download_for_viewers', (e.currentTarget as HTMLInputElement).checked)} />
      <span>
        <span class="label">{translate($lang, 'space.noDownload')}</span>
        <span class="hint">{translate($lang, 'space.noDownloadHint')}</span>
      </span>
    </label>
    <label class="switch">
      <input type="checkbox" checked={values.allow_links} disabled={busy} onchange={(e) => save('allow_links', (e.currentTarget as HTMLInputElement).checked)} />
      <span>
        <span class="label">{translate($lang, 'space.links')}</span>
        <span class="hint">{translate($lang, 'space.linksHint')}</span>
      </span>
    </label>
  </div>
</Dialog>

<style>
  .settings {
    display: grid;
    gap: var(--space-4);
  }

  .live:empty {
    display: none;
  }

  .switch {
    display: flex;
    align-items: flex-start;
    gap: var(--space-3);
    min-height: 44px;
  }

  .switch input {
    width: 20px;
    height: 20px;
    margin-block-start: 2px;
  }

  .switch > span {
    display: grid;
    gap: var(--space-1);
  }

  .label {
    font-weight: 600;
  }

  .hint {
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }
</style>
