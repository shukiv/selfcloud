<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { tr } from '../lib/i18n';
  import { establish, signInWithLink } from '../lib/auth';
  import { errorMessage, isLinkFailure, type Message } from '../lib/errors';
  import { go } from '../lib/router';
  import { looksLikeToken, signInTarget, tokenFromFragment } from '../lib/signin';
  import Alert from '../components/Alert.svelte';

  // The token sits in the fragment, so it never reaches a server log. It is read and removed from
  // the address bar before any request is made. The page makes one attempt: a failure shows a
  // message and no retry of the same token.
  let error = $state<Message | null>(null);
  let working = $state(true);

  onMount(() => {
    const token = tokenFromFragment(window.location.hash);
    window.history.replaceState(null, '', window.location.pathname + window.location.search);
    void redeem(token);
  });

  async function redeem(token: string) {
    if (!looksLikeToken(token)) {
      error = { key: 'signin.failed' };
      working = false;
      return;
    }
    try {
      const signed = await signInWithLink(token);
      await establish(signed);
      go(signInTarget(signed.next), { replace: true });
    } catch (err) {
      error = isLinkFailure(err) ? { key: 'signin.failed' } : errorMessage(err, 'link');
    } finally {
      working = false;
    }
  }
</script>

<h1 class="page-title">{$tr('signin.title')}</h1>

{#if working}
  <p class="muted" role="status">{$tr('signin.working')}</p>
{/if}

<Alert message={error} />
