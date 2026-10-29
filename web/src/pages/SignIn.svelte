<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate, tr } from '../lib/i18n';
  import { establish, signInWithLink } from '../lib/auth';
  import { errorMessage, isLinkFailure, type Message } from '../lib/errors';
  import { go } from '../lib/router';
  import {
    looksLikeToken,
    signInTarget,
    splitParts,
    switchPromptOf,
    tokenFromFragment,
    type SwitchPrompt,
  } from '../lib/signin';
  import Alert from '../components/Alert.svelte';
  import Dialog from '../components/Dialog.svelte';

  // The token sits in the fragment, so it never reaches a server log. It is read and removed from
  // the address bar before any request is made, and kept here only for a switch the person confirms.
  // The page makes one attempt: a failure shows a message and no retry of the same token.
  let token = $state('');
  let error = $state<Message | null>(null);
  let working = $state(true);
  // prompt is set when this browser is signed in as another user. Nothing has changed yet: the
  // person chooses to switch, or to stay.
  let prompt = $state<SwitchPrompt | null>(null);

  onMount(() => {
    token = tokenFromFragment(window.location.hash);
    window.history.replaceState(null, '', window.location.pathname + window.location.search);
    void redeem(token, false);
  });

  async function redeem(value: string, confirm: boolean) {
    if (!looksLikeToken(value)) {
      error = { key: 'signin.failed' };
      working = false;
      return;
    }
    prompt = null;
    working = true;
    try {
      const signed = await signInWithLink(value, confirm);
      await establish(signed);
      go(signInTarget(signed.next), { replace: true });
    } catch (err) {
      const asked = switchPromptOf(err);
      if (asked) {
        prompt = asked;
      } else {
        error = isLinkFailure(err) ? { key: 'signin.failed' } : errorMessage(err, 'link');
      }
    } finally {
      working = false;
    }
  }

  // stay keeps the browser as it is, and does not use the link. Escape does the same.
  function stay() {
    prompt = null;
    go('/files', { replace: true });
  }
</script>

<h1 class="page-title">{$tr('signin.title')}</h1>

{#if working}
  <p class="muted" role="status">{$tr('signin.working')}</p>
{/if}

<Alert message={error} />

{#if prompt}
  {@const names = { current: prompt.current, target: prompt.target }}
  <Dialog title={$tr('signin.switch.title')} id="signin-switch" onclose={stay}>
    <div class="switch">
      <p>
        {#each splitParts(translate($lang, 'signin.switch.message'), names) as part, i (i)}
          {#if part.name}<bdi>{part.text}</bdi>{:else}{part.text}{/if}
        {/each}
      </p>
      <div class="foot">
        <button type="button" class="btn primary" onclick={() => redeem(token, true)}>
          {#each splitParts(translate($lang, 'signin.switch.confirm'), names) as part, i (i)}
            {#if part.name}<bdi>{part.text}</bdi>{:else}{part.text}{/if}
          {/each}
        </button>
        <button type="button" class="btn secondary" onclick={stay}>
          {#each splitParts(translate($lang, 'signin.switch.stay'), names) as part, i (i)}
            {#if part.name}<bdi>{part.text}</bdi>{:else}{part.text}{/if}
          {/each}
        </button>
      </div>
    </div>
  </Dialog>
{/if}

<style>
  .switch {
    display: grid;
    gap: var(--space-3);
  }

  .switch p {
    margin: 0;
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
    margin-block-start: var(--space-2);
  }
</style>
