<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { failureText } from '../../lib/manage/failure';
  import { getSharingPolicy, setSharingPolicy } from '../../lib/sharing/api';

  // PublicLinksSwitch is the account's switch for links that anyone can open. Turning it off pauses those
  // links at once; turning it on makes them work again. The owner and the installation admin both use it.
  let { account }: { account: string } = $props();

  let on = $state(false);
  let anyoneLinks = $state(0);
  let loaded = $state(false);
  let busy = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);

  onMount(() => {
    void load();
  });

  async function load(): Promise<void> {
    try {
      const policy = await getSharingPolicy(account);
      on = policy.public_links;
      anyoneLinks = policy.anyone_links;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loaded = true;
    }
  }

  async function toggle(next: boolean): Promise<void> {
    busy = true;
    error = null;
    notice = null;
    try {
      const policy = await setSharingPolicy(account, next);
      on = policy.public_links;
      anyoneLinks = policy.anyone_links;
      notice = translate($lang, 'policy.saved');
    } catch (err) {
      error = failureText(err, $lang);
      on = !next;
    } finally {
      busy = false;
    }
  }
</script>

<section class="panel-plain" aria-labelledby="policy-public-links-title">
  <h2 id="policy-public-links-title" class="section-title">{translate($lang, 'policy.title')}</h2>
  {#if error}
    <p class="alert error" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="hint" role="status">{notice}</p>
  {/if}
  {#if loaded}
    <label class="check">
      <input
        id="public-links-switch"
        type="checkbox"
        checked={on}
        disabled={busy}
        onchange={(e) => toggle((e.currentTarget as HTMLInputElement).checked)}
      />
      <span>{translate($lang, 'policy.publicLinks')}</span>
    </label>
    <p class="hint">{translate($lang, 'policy.publicLinksHint')}</p>
    <p class="hint">{translatePlural($lang, 'policy.anyoneLinks', anyoneLinks)}</p>
  {/if}
</section>

<style>
  .section-title {
    margin: 0 0 var(--space-2);
    font-size: var(--text-lg);
    font-weight: 600;
  }
</style>
