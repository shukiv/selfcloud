<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { brand } from '../lib/brand';
  import { tr } from '../lib/i18n';
  import { flash } from '../lib/session';
  import Alert from './Alert.svelte';
  import LangSwitch from './LangSwitch.svelte';
  import ThemeSwitch from './ThemeSwitch.svelte';

  let { children }: { children: Snippet } = $props();
</script>

<a class="skip-link" href="#main">{$tr('app.skip')}</a>
<main id="main" class="public" tabindex="-1">
  <header class="public-head">
    {#if $brand.logo_url}
      <img src={$brand.logo_url} alt="" width="40" height="40" />
    {/if}
    <p class="product-name"><bdi>{$brand.name}</bdi></p>
  </header>
  {#if $flash}
    <div class="notice"><Alert message={{ key: $flash }} tone="success" /></div>
  {/if}
  <div class="panel">
    {@render children()}
  </div>
  <div class="controls">
    <LangSwitch />
    <ThemeSwitch />
  </div>
</main>
