<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { tr } from '../lib/i18n';
  import { session } from '../lib/session';
  import { go, signInURL } from '../lib/router';
  import { openShareLink } from '../lib/sharing/api';
  import { forgetSecret, openStep, peekSecret } from '../lib/sharing/links';
  import { type Message } from '../lib/errors';
  import Alert from '../components/Alert.svelte';

  // OpenLink opens a shared item from the address /s#secret. The secret was moved out of the address, into this
  // tab's session storage, before the page loaded. A signed-in member opens it at once; a signed-out visitor goes
  // to sign-in, and comes back here with the secret still waiting. The secret is removed once it is used, and
  // when the open fails. Every dead link gets the one message.
  let working = $state(true);
  let message = $state<Message | null>(null);

  onMount(() => {
    void run();
  });

  async function run(): Promise<void> {
    const secret = peekSecret(window.sessionStorage);
    const step = openStep(secret, $session !== null);
    if (step === 'dead') {
      message = { key: 'share.open.dead' };
      working = false;
      return;
    }
    if (step === 'sign-in') {
      go(signInURL('/s'), { replace: true });
      return;
    }
    forgetSecret(window.sessionStorage);
    try {
      const opened = await openShareLink(secret);
      go(`/n/${opened.node_id}`, { replace: true });
    } catch {
      message = { key: 'share.open.dead' };
      working = false;
    }
  }
</script>

<h1 class="page-title">{$tr('openlink.title')}</h1>

{#if working}
  <p class="muted" role="status">{$tr('openlink.working')}</p>
{/if}

<Alert {message} />
