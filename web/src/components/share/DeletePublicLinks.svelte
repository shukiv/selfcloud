<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { failureText } from '../../lib/manage/failure';
  import { canDeleteAll, deleteAllCount } from '../../lib/manage/links';
  import { deleteAllPublicLinks } from '../../lib/manage/api';
  import Confirm from '../Confirm.svelte';

  // DeletePublicLinks deletes every link that anyone can open, and every drop link, of an account. It is one
  // deliberate action: it asks first, and the confirmation names the count. Links that only people in the account
  // can open are kept.
  let {
    account,
    count,
    ondone,
  }: { account: string; count: number; ondone: (deleted: number) => void } = $props();

  let asking = $state(false);
  let busy = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);

  let n = $derived(deleteAllCount(count));

  async function confirmDelete(): Promise<void> {
    busy = true;
    error = null;
    try {
      const reply = await deleteAllPublicLinks(account);
      asking = false;
      notice = translatePlural($lang, 'links.deleteAll.done', reply.deleted);
      ondone(reply.deleted);
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busy = false;
    }
  }
</script>

{#if canDeleteAll(count, busy)}
  <p class="toolbar-actions">
    <button type="button" class="btn danger-fill" onclick={() => (asking = true)}>
      {translate($lang, 'links.deleteAll.action')}
    </button>
  </p>
{/if}
{#if notice}
  <p class="hint" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if asking}
  <Confirm
    id="delete-all-public-links"
    title={translate($lang, 'links.deleteAll.title')}
    message={translatePlural($lang, 'links.deleteAll.message', n)}
    action={translate($lang, 'links.deleteAll.action')}
    danger={true}
    {busy}
    {error}
    onconfirm={() => void confirmDelete()}
    onclose={() => (asking = false)}
  />
{/if}
